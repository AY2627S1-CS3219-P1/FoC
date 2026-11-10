package images

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type S3Config struct{ Bucket, Region, Endpoint, PublicEndpoint string }

type S3 struct {
	client                 *s3.Client
	signer                 *s3.PresignClient
	bucket                 string
	proofScope, grantScope string
	probeEndpoint          string
}

// NewS3 uses the standard AWS credential chain. Separate endpoints allow local
// container traffic and browser grants to use their actual, signed hostnames.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
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
	create := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
				o.UsePathStyle = true
			}
		})
	}
	client := create(c.Endpoint)
	public := c.PublicEndpoint
	if public == "" {
		public = c.Endpoint
	}
	return &S3{
		client:        client,
		signer:        s3.NewPresignClient(create(public)),
		bucket:        c.Bucket,
		proofScope:    c.Bucket + "|" + c.Region + "|" + c.Endpoint,
		grantScope:    c.Bucket + "|" + c.Region + "|" + public,
		probeEndpoint: c.Endpoint,
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
	}, func(o *s3.PresignOptions) { o.Expires = 10 * time.Minute })
	if err != nil {
		return UploadGrant{}, err
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
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}, nil
}

func (s *S3) Inspect(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey") {
			return ObjectInfo{}, ErrObjectNotFound
		}
		return ObjectInfo{}, err
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
		func(o *s3.PresignOptions) { o.Expires = 5 * time.Minute },
	)
	if err != nil {
		return DownloadGrant{}, err
	}
	return DownloadGrant{URL: out.URL, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

// InitializeLocalBucket is an explicit local setup action, never server startup
// behavior. Production buckets and policies are provisioned outside this service.
// Browsers on the default frontend port and any extra origins may upload.
func (s *S3) InitializeLocalBucket(ctx context.Context, origins ...string) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var api smithy.APIError
	if err != nil && !(errors.As(err, &api) && api.ErrorCode() == "BucketAlreadyOwnedByYou") {
		return err
	}
	_, err = s.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(s.bucket),
		CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{
			AllowedMethods: []string{"PUT", "GET", "HEAD"},
			AllowedOrigins: append([]string{"http://localhost:5173", "http://127.0.0.1:5173"}, origins...),
			AllowedHeaders: []string{"content-type", "if-none-match"},
			ExposeHeaders:  []string{"ETag"},
			MaxAgeSeconds:  aws.Int32(300),
		}}},
	})
	return err
}

var _ ObjectStorage = (*S3)(nil)
