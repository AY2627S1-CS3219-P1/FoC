//go:build integration

package config_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

type evaluator struct{ fail bool }

func (e *evaluator) Evaluate(context.Context, jobs.EvaluationInput) (jobs.Assessment, error) {
	if e.fail {
		return jobs.Assessment{}, errors.New("provider unavailable")
	}
	return jobs.Assessment{Outcome: jobs.OutcomeApproved}, nil
}

func TestActivationValidatesAndRejectsStaleWriters(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool := database.Connect(url)
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE moderation_jobs, moderation_attempts, moderation_outbox,
		moderation_quarantine, moderation_config_versions, moderation_config_state, moderation_admin_audit`); err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(pool)
	if err := store.EnsureConfigState(ctx); err != nil {
		t.Fatal(err)
	}
	eval := &evaluator{}
	service := config.NewService(
		store,
		nil,
		eval,
		jobs.NewLimiter(1),
		config.Settings{PolicyVersion: "review-text-v1"},
		time.Now,
	)
	first, err := service.SaveDraft(ctx, "model", config.OutputModeJSONObject, config.InferenceParameters{}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveDraft(
		ctx,
		"other-model",
		config.OutputModeJSONObject,
		config.InferenceParameters{},
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, first, "admin", nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, second, "admin", nil); !errors.Is(err, config.ErrConfigurationConflict) {
		t.Fatalf("stale first activation: %v", err)
	}
	eval.fail = true
	if err := service.Activate(ctx, second, "admin", &first); err == nil {
		t.Fatal("failed provider validation activated draft")
	}
	active, err := service.ActiveConfiguration(ctx)
	if err != nil || active.VersionID != first {
		t.Fatalf("failed validation changed active config: %+v, %v", active, err)
	}
	eval.fail = false
	if err := service.Activate(ctx, second, "admin", &first); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_admin_audit WHERE action = 'ACTIVATE_CONFIG'`).
		Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("activation audit count = %d, want 2", audits)
	}
}
