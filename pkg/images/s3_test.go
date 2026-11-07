package images

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/aws/smithy-go"
)

func apiErr(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func TestClassifyStorageError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error // sentinel to match via errors.Is; nil means passthrough
	}{
		{"AccessDenied is misconfigured", apiErr("AccessDenied"), ErrStorageMisconfigured},
		{"NoSuchBucket is misconfigured", apiErr("NoSuchBucket"), ErrStorageMisconfigured},
		{"InvalidAccessKeyId is misconfigured", apiErr("InvalidAccessKeyId"), ErrStorageMisconfigured},
		{"SlowDown is transient", apiErr("SlowDown"), ErrStorageUnavailable},
		{"ServiceUnavailable is transient", apiErr("ServiceUnavailable"), ErrStorageUnavailable},
		{"deadline is transient", context.DeadlineExceeded, ErrStorageUnavailable},
		{
			"connection fault is transient",
			&net.OpError{Op: "dial", Err: errors.New("connection refused")},
			ErrStorageUnavailable,
		},
		{"unknown smithy code passes through", apiErr("SomeFutureCode"), nil},
		{"canceled passes through", context.Canceled, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyStorageError(tc.err)
			if tc.want == nil {
				if !errors.Is(got, tc.err) {
					t.Fatalf("want passthrough of %v, got %v", tc.err, got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			// The original failure must stay in the chain for logs and
			// downstream errors.Is checks.
			if !errors.Is(got, tc.err) {
				t.Fatalf("original error lost from the chain: %v", got)
			}
		})
	}
}
