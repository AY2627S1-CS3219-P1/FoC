package images

import (
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// ReadProof extends the existing S3 adapter with bounded server-side reading for
// proof validation. It uses the same client, bucket and credentials as item
// images; it is not another upload or signing system.
func (s *S3) ReadProof(ctx context.Context, key string, maxBytes int64) (io.ReadCloser, error) {
	if key == "" || maxBytes < 1 {
		return nil, errors.New("proof key and positive read bound are required")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey") {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	if aws.ToInt64(out.ContentLength) > maxBytes {
		_ = out.Body.Close()
		return nil, errors.New("proof object exceeds read bound")
	}
	return &boundedProofBody{Reader: io.LimitReader(out.Body, maxBytes+1), closer: out.Body}, nil
}

type boundedProofBody struct {
	io.Reader
	closer io.Closer
}

func (b *boundedProofBody) Close() error { return b.closer.Close() }
