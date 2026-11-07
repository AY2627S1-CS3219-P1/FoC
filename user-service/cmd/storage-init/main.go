// storage-init provisions the local review photo bucket and browser CORS policy.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
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
			Bucket:   os.Getenv("USER_S3_BUCKET"),
			Endpoint: os.Getenv("USER_S3_ENDPOINT"),
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
			slog.Info("Local review photo bucket and CORS initialized")
			return
		}
		select {
		case <-ctx.Done():
			slog.Error("local review photo storage did not become ready")
			os.Exit(1)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// originOf returns the origin a browser sends for a frontend URL: the scheme and
// lowercase host, without a path or the scheme's default port.
func originOf(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", base)
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port), nil
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, nil
}
