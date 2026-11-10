// Smoke test for pkg/images against a live FloCI S3 endpoint. Run manually:
//
//	FLOCI_SMOKE_ENDPOINT=http://localhost:45660 go run ./cmd/storage-init-smoke
//
// Exercises bucket initialization, a pre-signed PUT roundtrip, metadata
// inspection and a pre-signed GET — the exact adapter path review photos use.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
)

func main() {
	endpoint := os.Getenv("FLOCI_SMOKE_ENDPOINT")
	if endpoint == "" {
		log.Fatal("set FLOCI_SMOKE_ENDPOINT")
	}
	// FloCI accepts static test credentials; the standard chain picks these up.
	if err := os.Setenv("AWS_ACCESS_KEY_ID", "test"); err != nil {
		log.Fatal("set AWS_ACCESS_KEY_ID: ", err)
	}
	if err := os.Setenv("AWS_SECRET_ACCESS_KEY", "test"); err != nil {
		log.Fatal("set AWS_SECRET_ACCESS_KEY: ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	storage, err := images.NewS3(ctx, images.S3Config{
		Bucket:         "user-images",
		Endpoint:       endpoint,
		PublicEndpoint: endpoint,
		Region:         "us-east-1",
	})
	if err != nil {
		log.Fatal("NewS3: ", err)
	}

	// Wait for FloCI readiness, then initialize bucket + CORS like storage-init.
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = storage.InitializeLocalBucket(ctx, "http://localhost:5173")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			log.Fatal("InitializeLocalBucket: ", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("PASS InitializeLocalBucket")

	key := "review/photo/smoke-" + fmt.Sprint(time.Now().UnixNano())
	grant, err := storage.CreateUpload(ctx, images.UploadRequest{
		Key: key, ContentType: "image/png", SizeBytes: 8,
	})
	if err != nil {
		log.Fatal("CreateUpload: ", err)
	}
	if grant.URL == "" || grant.Method != "PUT" || len(grant.Headers) == 0 {
		log.Fatalf("unexpected grant: %+v", grant)
	}
	fmt.Println("PASS CreateUpload (pre-signed PUT grant)")

	// Reject a shape that violates the signed contract.
	bad, err := http.NewRequestWithContext(ctx, "PUT", grant.URL, bytes.NewReader(make([]byte, 9)))
	if err != nil {
		log.Fatal("build oversized PUT: ", err)
	}
	bad.Header.Set("Content-Type", "image/png")
	if resp, err := http.DefaultClient.Do(bad); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			log.Fatal("oversized PUT must be rejected by storage")
		}
	}
	fmt.Println("PASS rejected PUT outside the signed shape")

	put, err := http.NewRequestWithContext(ctx, "PUT", grant.URL, bytes.NewReader([]byte("PNGDATA!")))
	if err != nil {
		log.Fatal("build PUT: ", err)
	}
	for k, v := range grant.Headers {
		put.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		log.Fatal("PUT: ", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatal("PUT status: ", resp.StatusCode)
	}
	fmt.Println("PASS direct PUT with grant")

	// Idempotency: re-PUT must fail (immutable object).
	resp, err = http.DefaultClient.Do(put)
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			log.Fatal("overwrite must fail with If-None-Match")
		}
	}
	fmt.Println("PASS immutable object (re-PUT rejected)")

	info, err := storage.Inspect(ctx, key)
	if err != nil || !info.Exists || info.Size != 8 || info.ContentType != "image/png" {
		log.Fatalf("Inspect mismatch: %+v err=%v", info, err)
	}
	fmt.Println("PASS Inspect (stored metadata matches the grant)")

	// Absent key reports ErrObjectNotFound, not a storage failure.
	if _, err := storage.Inspect(ctx, key+"-absent"); err == nil {
		log.Fatal("absent key must return ErrObjectNotFound")
	} else if err.Error() != images.ErrObjectNotFound.Error() {
		log.Fatal("absent key error must be ErrObjectNotFound, got: ", err)
	}
	fmt.Println("PASS Inspect absent key")

	download, err := storage.CreateDownload(ctx, key)
	if err != nil || download.URL == "" {
		log.Fatal("CreateDownload: ", err)
	}
	get, err := http.NewRequestWithContext(ctx, "GET", download.URL, nil)
	if err != nil {
		log.Fatal("build GET: ", err)
	}
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		log.Fatal("GET: ", err)
	}
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		log.Fatal("read GET body: ", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || body.String() != "PNGDATA!" {
		log.Fatalf("GET roundtrip mismatch: status=%d body=%q", resp.StatusCode, body.String())
	}
	fmt.Println("PASS pre-signed GET roundtrip")

	if err := storage.Delete(ctx, key); err != nil {
		log.Fatal("Delete: ", err)
	}
	if _, err := storage.Inspect(ctx, key); err == nil {
		log.Fatal("deleted object must be gone")
	}
	fmt.Println("PASS Delete")
	fmt.Println("SMOKE OK: every adapter path validated against live FloCI")
}
