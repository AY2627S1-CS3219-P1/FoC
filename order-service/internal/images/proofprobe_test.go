package images

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Certification uses the public signed PUT path, not an internal HEAD result.
func TestProofProbeRequiresExplicitUploadServiceFailure(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "probe-test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "probe-test-secret")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusForbidden, http.StatusTemporaryRedirect, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var puts, deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPut {
					t.Errorf("probe used %s, not PUT", r.Method)
					w.WriteHeader(400)
					return
				}
				puts.Add(1)
				if r.URL.Query().Get("X-Amz-Signature") == "" || r.Header.Get("If-None-Match") != "*" {
					t.Error("probe bypassed actual signed immutable upload path")
				}
				if _, err := png.Decode(r.Body); err != nil {
					t.Error(err)
				}
				if status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "http://127.0.0.1:1/should-not-follow")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			store, err := NewS3(
				t.Context(),
				S3Config{Bucket: "probe-tests", Region: "us-east-1", Endpoint: server.URL, PublicEndpoint: server.URL},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := store.ProbeProofUpload(t.Context(), "order/proof/probe/fixed")
			if result.Unavailable != (status >= 500) {
				t.Fatalf("status %d certified=%v", status, result.Unavailable)
			}
			if puts.Load() != 1 {
				t.Fatalf("unexpected probe or redirect retries: %d", puts.Load())
			}
			if status == http.StatusOK && (err != nil || deletes.Load() != 1) {
				t.Fatalf("successful probe not cleaned: %d %v", deletes.Load(), err)
			}
			if status == http.StatusForbidden && err == nil {
				t.Fatal("authentication failure not distinguished")
			}
		})
	}
}
