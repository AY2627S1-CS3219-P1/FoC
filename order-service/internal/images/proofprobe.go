package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"time"
)

type ProofProbeResult struct {
	// Only an explicit storage-service 5xx response certifies PUT unavailability.
	// Authentication, signing, routing and transport failures do not.
	Unavailable bool
	CleanupKey  string
}

// ProbeProofUpload exercises the actual signed browser upload path, not HEAD or
// the internal SDK endpoint. A failed cleanup must be retried before probing
// again so a permissions error cannot continuously accumulate probe objects.
func (s *S3) ProbeProofUpload(ctx context.Context, key string) (ProofProbeResult, error) {
	epoch, expiry, err := s.ProofSigningEpoch(ctx)
	if err != nil {
		return ProofProbeResult{}, errors.New("proof probe credentials unavailable")
	}
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(10 * time.Second)
	if !expiry.IsZero() && expiry.Before(until) {
		until = expiry
	}
	var body bytes.Buffer
	if err = png.Encode(&body, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return ProofProbeResult{}, errors.New("proof probe image unavailable")
	}
	grant, err := s.CreateFixedUpload(ctx, FixedUploadRequest{
		UploadRequest: UploadRequest{Key: key, ContentType: "image/png", SizeBytes: int64(body.Len())},
		SigningEpoch:  epoch,
		GrantedAt:     now,
		ExpiresAt:     until,
	})
	if err != nil {
		return ProofProbeResult{}, errors.New("proof probe signing unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.URL, bytes.NewReader(body.Bytes()))
	if err != nil {
		return ProofProbeResult{}, errors.New("proof probe request unavailable")
	}
	// In local containers the browser's loopback endpoint is not this process's
	// loopback. Probe the same configured store while retaining the signed
	// browser Host. Never rewrite a public HTTPS endpoint or client input.
	internal, parseErr := url.Parse(s.probeEndpoint)
	host := request.URL.Hostname()
	if parseErr == nil && internal.Host != "" && internal.Scheme == "http" && request.URL.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		request.Host = request.URL.Host
		request.URL.Host = internal.Host
	}

	for key, value := range grant.Headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ProofProbeResult{CleanupKey: key}, errors.New("proof upload path probe could not be confirmed")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if response.StatusCode >= 500 && response.StatusCode <= 599 {
		return ProofProbeResult{Unavailable: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProofProbeResult{}, errors.New("proof probe refused by upload service")
	}
	if err = s.Delete(ctx, key); err != nil {
		return ProofProbeResult{CleanupKey: key}, errors.New("proof probe cleanup requires retry")
	}
	return ProofProbeResult{}, nil
}
