package images

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The upload contract requires identical live grants across replicas, signed
// exact length/type/immutable PUT, no forbidden browser headers, and rotation
// invalidation. This uses the real SDK without making storage/network requests.
func TestS3FixedProofGrantAcrossReplicasAndRotation(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "proof-test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "proof-test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	cfg := S3Config{Bucket: "proof-tests", Region: "us-east-1", Endpoint: "http://127.0.0.1:4566", PublicEndpoint: "http://localhost:4566"}
	first, err := NewS3(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewS3(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	epoch, _, err := first.ProofSigningEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	input := FixedUploadRequest{
		UploadRequest: UploadRequest{Key: "order/proof/stable", ContentType: "image/heic", SizeBytes: 8000000},
		GrantedAt:     at,
		ExpiresAt:     at.Add(10 * time.Minute),
		SigningEpoch:  epoch,
	}
	one, err := first.CreateFixedUpload(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.CreateFixedUpload(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) {
		t.Fatal("replicas changed the durable grant")
	}
	parsed, err := url.Parse(one.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "localhost:4566" || parsed.Query().Get("X-Amz-Date") != at.Format("20060102T150405Z") || parsed.Query().Get("X-Amz-Expires") != "600" {
		t.Fatal("wrong public endpoint, signing instant or lifetime")
	}
	signed := parsed.Query().Get("X-Amz-SignedHeaders")
	for _, header := range []string{"content-length", "content-type", "if-none-match"} {
		if !strings.Contains(signed, header) {
			t.Fatalf("missing signed upload constraint: %s", header)
		}
	}
	for header := range one.Headers {
		if strings.EqualFold(header, "Host") || strings.EqualFold(header, "Content-Length") {
			t.Fatal("forbidden browser header")
		}
	}
	changedConfig := cfg
	changedConfig.PublicEndpoint = "http://localhost:9876"
	moved, err := NewS3(t.Context(), changedConfig)
	if err != nil {
		t.Fatal(err)
	}
	movedEpoch, _, err := moved.ProofSigningEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if moved.ProofScope() != first.ProofScope() || moved.ProofOutageScope() == first.ProofOutageScope() || movedEpoch == epoch {
		t.Fatal("browser route must rotate grants/outage evidence without changing stored-byte identity")
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "rotated-test-secret")
	rotated, err := NewS3(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	newEpoch, _, err := rotated.ProofSigningEpoch(t.Context())
	if err != nil || newEpoch == epoch {
		t.Fatal("credential rotation did not change epoch")
	}
	if _, err = rotated.CreateFixedUpload(t.Context(), input); err == nil {
		t.Fatal("stale signing credentials accepted")
	}
}
