package images

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

var misconfiguredCodes = map[string]bool{
	"AccessDenied":                       true,
	"NoSuchBucket":                       true,
	"InvalidAccessKeyId":                 true,
	"SignatureDoesNotMatch":              true,
	"AuthorizationHeaderMalformed":       true,
	"AuthorizationQueryParametersError":  true,
	"PermanentRedirect":                  true,
	"InvalidToken":                       true,
	"ExpiredToken":                       true,
	"IllegalLocationConstraintException": true,
}

var transientCodes = map[string]bool{
	"SlowDown":           true,
	"ServiceUnavailable": true,
	"InternalError":      true,
	"RequestTimeout":     true,
}

// Caller cancellation and unclassified errors pass through unchanged.
func classifyStorageError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrStorageUnavailable, err)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		switch {
		case transientCodes[code]:
			return fmt.Errorf("%w: %w", ErrStorageUnavailable, err)
		case misconfiguredCodes[code]:
			return fmt.Errorf("%w: %w", ErrStorageMisconfigured, err)
		}
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %w", ErrStorageUnavailable, err)
	}
	return err
}

// Default lifetimes for presigned grants when the caller does not set them.
const (
	DefaultUploadTTL   = 10 * time.Minute
	DefaultDownloadTTL = 5 * time.Minute
)

// UploadTTL and DownloadTTL bound presigned grants; zero uses the defaults, negative is rejected.
type S3Config struct {
	Bucket, Region, Endpoint, PublicEndpoint string
	UploadTTL, DownloadTTL                   time.Duration
}

type S3 struct {
	client      *s3.Client
	signer      *s3.PresignClient
	bucket      string
	uploadTTL   time.Duration
	downloadTTL time.Duration
}

// newS3Client uses path-style addressing whenever a custom endpoint is set.
func newS3Client(cfg aws.Config, endpoint string) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint == "" {
			return
		}
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

// NewS3 uses the standard credential chain; PublicEndpoint signs browser URLs.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if c.UploadTTL < 0 || c.DownloadTTL < 0 {
		return nil, errors.New("presign TTLs must not be negative")
	}
	uploadTTL := c.UploadTTL
	if uploadTTL == 0 {
		uploadTTL = DefaultUploadTTL
	}
	downloadTTL := c.DownloadTTL
	if downloadTTL == 0 {
		downloadTTL = DefaultDownloadTTL
	}
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(c.Region),
		config.WithHTTPClient(
			&http.Client{
				Timeout:       5 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
		),
	)
	if err != nil {
		return nil, err
	}
	cfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	public := c.PublicEndpoint
	if public == "" {
		public = c.Endpoint
	}
	return &S3{
		client:      newS3Client(cfg, c.Endpoint),
		signer:      s3.NewPresignClient(newS3Client(cfg, public)),
		bucket:      c.Bucket,
		uploadTTL:   uploadTTL,
		downloadTTL: downloadTTL,
	}, nil
}

func (s *S3) CreateUpload(ctx context.Context, in UploadRequest) (UploadGrant, error) {
	if in.Key == "" || in.SizeBytes < 1 {
		return UploadGrant{}, errors.New("upload key and positive size are required")
	}
	grant, err := s.signer.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(in.Key),
		ContentType:   aws.String(in.ContentType),
		ContentLength: aws.Int64(in.SizeBytes),
		IfNoneMatch:   aws.String("*"),
	}, func(o *s3.PresignOptions) { o.Expires = s.uploadTTL })
	if err != nil {
		return UploadGrant{}, classifyStorageError(err)
	}
	headers := map[string]string{}
	for key, values := range grant.SignedHeader {
		// Fetch sets these automatically; JavaScript cannot set them explicitly.
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Host") {
			continue
		}
		if len(values) != 0 {
			headers[key] = values[0]
		}
	}
	return UploadGrant{
		URL:       grant.URL,
		Method:    grant.Method,
		Headers:   headers,
		ExpiresAt: time.Now().Add(s.uploadTTL),
	}, nil
}

func (s *S3) Inspect(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey") {
			return ObjectInfo{}, ErrObjectNotFound
		}
		return ObjectInfo{}, classifyStorageError(err)
	}
	return ObjectInfo{
		Exists:      true,
		Size:        aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType),
	}, nil
}

func (s *S3) CreateDownload(ctx context.Context, key string) (DownloadGrant, error) {
	out, err := s.signer.PresignGetObject(
		ctx,
		&s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)},
		func(o *s3.PresignOptions) { o.Expires = s.downloadTTL },
	)
	if err != nil {
		return DownloadGrant{}, classifyStorageError(err)
	}
	return DownloadGrant{URL: out.URL, ExpiresAt: time.Now().Add(s.downloadTTL)}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

// InitializeLocalBucket creates the bucket and CORS policy from the caller's origins.
func (s *S3) InitializeLocalBucket(ctx context.Context, origins ...string) error {
	if len(origins) == 0 {
		return errors.New("at least one browser origin is required")
	}
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var api smithy.APIError
	if err != nil && !(errors.As(err, &api) && api.ErrorCode() == "BucketAlreadyOwnedByYou") {
		return err
	}
	_, err = s.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(s.bucket),
		CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{
			AllowedMethods: []string{"PUT", "GET", "HEAD"},
			AllowedOrigins: origins,
			AllowedHeaders: []string{"content-type", "if-none-match"},
			ExposeHeaders:  []string{"ETag"},
			MaxAgeSeconds:  aws.Int32(300),
		}}},
	})
	return err
}

var _ ObjectStorage = (*S3)(nil)
