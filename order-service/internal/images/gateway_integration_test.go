package images

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// TestMeshObjectStorage uses the real gateway for signed/unsigned object access.
// A private operator forward is used only for metadata inspection and cleanup.
func TestMeshObjectStorage(t *testing.T) {
	endpoint := os.Getenv("S3_SMOKE_ENDPOINT")
	if endpoint == "" {
		t.Skip("set S3_SMOKE_ENDPOINT, S3_SMOKE_PUBLIC_ENDPOINT and S3_SMOKE_CA_FILE")
	}
	cert, err := os.ReadFile(os.Getenv("S3_SMOKE_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("missing gateway trust certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	storage, err := NewS3(ctx, S3Config{
		Bucket: "order-images", Region: "us-east-1", Endpoint: endpoint,
		PublicEndpoint: os.Getenv("S3_SMOKE_PUBLIC_ENDPOINT"),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "_infrastructure-smoke/" + rand.Text() + ".bin"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := storage.Delete(cleanup, key); err != nil {
			t.Errorf("cleanup smoke object: %v", err)
		}
	}()
	payload := []byte("FoC mesh storage smoke")
	grant, err := storage.CreateUpload(ctx, UploadRequest{Key: key, ContentType: "application/octet-stream", SizeBytes: int64(len(payload))})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, grant.Method, grant.URL, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range grant.Headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("signed upload transport failed (URL redacted)")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("signed upload: HTTP %d", response.StatusCode)
	}
	info, err := storage.Inspect(ctx, key)
	if err != nil || info.Size != int64(len(payload)) {
		t.Fatalf("uploaded metadata: %+v, %v", info, err)
	}
	download, err := storage.CreateDownload(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(download.URL)
	if err != nil {
		t.Fatal("signed download transport failed (URL redacted)")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, payload) {
		t.Fatalf("signed download: HTTP %d, body matches=%v, error=%v", response.StatusCode, bytes.Equal(body, payload), err)
	}
	unsigned, err := url.Parse(download.URL)
	if err != nil {
		t.Fatal(err)
	}
	unsigned.RawQuery = ""
	tampered, err := url.Parse(download.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := tampered.Query()
	query.Set("X-Amz-Signature", strings.Repeat("0", 64))
	tampered.RawQuery = query.Encode()
	for name, target := range map[string]string{"unsigned": unsigned.String(), "tampered": tampered.String()} {
		response, err := client.Get(target)
		if err != nil {
			t.Fatalf("%s object access transport failed (URL redacted)", name)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s object access: expected 403, got %d", name, response.StatusCode)
		}
	}
	// Prove the bucket-root boundary independently of S3 signature rejection.
	// The same valid header credential may inspect an object, but not the bucket.
	digest := sha256.Sum256(nil)
	payloadHash := hex.EncodeToString(digest[:])
	for _, probe := range []struct {
		path   string
		status int
	}{
		{path: "/order-images/" + key, status: http.StatusOK},
		{path: "/order-images/", status: http.StatusForbidden},
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, os.Getenv("S3_SMOKE_PUBLIC_ENDPOINT")+probe.path, nil)
		if err != nil {
			t.Fatal("invalid storage probe origin")
		}
		request.Header.Set("X-Amz-Content-Sha256", payloadHash)
		credentials := aws.Credentials{
			AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		}
		err = v4.NewSigner().SignHTTP(ctx, credentials, request, payloadHash, "s3", "us-east-1", time.Now())
		if err != nil {
			t.Fatal("storage probe signing failed")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("signed storage HEAD failed (URL redacted)")
		}
		_ = response.Body.Close()
		if response.StatusCode != probe.status {
			t.Fatalf("storage HEAD expected %d, got %d", probe.status, response.StatusCode)
		}
	}
}
