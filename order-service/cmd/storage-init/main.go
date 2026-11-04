// storage-init provisions the local image bucket and browser CORS policy.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

func main() {
	if os.Getenv("APP_ENV") != "local" {
		slog.Error("storage-init is restricted to local development")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	storage, err := images.NewS3(
		ctx,
		images.S3Config{
			Bucket:   os.Getenv("ORDER_S3_BUCKET"),
			Endpoint: os.Getenv("ORDER_S3_ENDPOINT"),
			Region:   os.Getenv("AWS_REGION"),
		},
	)
	if err != nil {
		slog.Error("invalid local storage configuration")
		os.Exit(1)
	}
	var origins []string
	if base := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL")); base != "" {
		origin, err := originOf(base)
		if err != nil {
			slog.Error("invalid FRONTEND_BASE_URL", "err", err)
			os.Exit(1)
		}
		origins = append(origins, origin)
	}
	for {
		if err := storage.InitializeLocalBucket(ctx, origins...); err == nil {
			slog.Info("Local image bucket and CORS initialized")
			return
		}
		select {
		case <-ctx.Done():
			slog.Error("local image storage did not become ready")
			os.Exit(1)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// originOf returns the browser origin of a frontend URL: browsers send only the
// scheme and host, so a path would never match.
func originOf(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", base)
	}
	return u.Scheme + "://" + u.Host, nil
}
