package images

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ProofSigningEpoch identifies credential rotation without persisting a bearer
// URL or credentials. Expiring credentials also bound a grant's actual lifetime.
func (s *S3) ProofSigningEpoch(ctx context.Context) (string, time.Time, error) {
	credentials, err := s.client.Options().Credentials.Retrieve(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry := time.Time{}
	if credentials.CanExpire {
		expiry = credentials.Expires.UTC().Truncate(time.Second)
	}
	return proofEpoch(credentials, s.grantScope), expiry, nil
}

func proofEpoch(c aws.Credentials, scope string) string {
	sum := sha256.Sum256([]byte(c.AccessKeyID + "\x00" + c.SecretAccessKey + "\x00" + c.SessionToken + "\x00" + scope))
	return fmt.Sprintf("%x", sum)
}

type FixedUploadRequest struct {
	UploadRequest
	GrantedAt, ExpiresAt time.Time
	SigningEpoch         string
}

type fixedProofPresigner struct {
	at, expires time.Time
	epoch       string
	scope       string
}

func (p fixedProofPresigner) PresignHTTP(ctx context.Context, c aws.Credentials, r *http.Request, hash, service, region string, _ time.Time, options ...func(*v4.SignerOptions)) (string, http.Header, error) {
	if proofEpoch(c, p.scope) != p.epoch || (c.CanExpire && p.expires.After(c.Expires)) {
		return "", nil, fmt.Errorf("proof signing identity rotated or expired")
	}
	return v4.NewSigner().PresignHTTP(ctx, c, r, hash, service, region, p.at, options...)
}

// CreateFixedUpload uses the SDK's supported signer injection. Every signed
// request field, time and credential identity comes from the durable generation.
func (s *S3) CreateFixedUpload(ctx context.Context, in FixedUploadRequest) (UploadGrant, error) {
	if in.Key == "" || in.SizeBytes < 1 || in.SigningEpoch == "" || !in.ExpiresAt.After(in.GrantedAt) || !in.ExpiresAt.After(time.Now()) {
		return UploadGrant{}, fmt.Errorf("invalid fixed proof grant")
	}
	signed, err := s.signer.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(in.Key), ContentType: aws.String(in.ContentType), ContentLength: aws.Int64(in.SizeBytes), IfNoneMatch: aws.String("*"),
	}, func(o *s3.PresignOptions) {
		o.Expires = in.ExpiresAt.Sub(in.GrantedAt)
		o.Presigner = fixedProofPresigner{at: in.GrantedAt, expires: in.ExpiresAt, epoch: in.SigningEpoch, scope: s.grantScope}
	})
	if err != nil {
		return UploadGrant{}, err
	}
	headers := map[string]string{}
	for key, values := range signed.SignedHeader {
		if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return UploadGrant{URL: signed.URL, Method: signed.Method, Headers: headers, ExpiresAt: in.ExpiresAt}, nil
}

// ProofScope identifies stored bytes, independently of browser grant hostnames.
func (s *S3) ProofScope() string {
	sum := sha256.Sum256([]byte(s.proofScope))
	return fmt.Sprintf("%x", sum)
}

// ProofOutageScope binds certification to the current signed browser route.
func (s *S3) ProofOutageScope() string {
	sum := sha256.Sum256([]byte(s.proofScope + "|" + s.grantScope))
	return fmt.Sprintf("%x", sum)
}
