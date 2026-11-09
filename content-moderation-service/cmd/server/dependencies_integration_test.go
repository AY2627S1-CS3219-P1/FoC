//go:build integration

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
)

func TestComponentInitializationFailureAndRecovery(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool := database.Connect(url)
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DELETE FROM moderation_config_state"); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	components, err := newServerComponents(cancelled, pool, appConfig{}, slog.Default())
	if !errors.Is(err, context.Canceled) || components.service != nil {
		t.Fatalf("initialization did not fail: %+v, %v", components, err)
	}
	components, err = newServerComponents(ctx, pool, appConfig{}, slog.Default())
	if err != nil || components.service == nil {
		t.Fatalf("retry: %+v, %v", components, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM moderation_config_state").
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("configuration state: %d, %v", count, err)
	}
}
