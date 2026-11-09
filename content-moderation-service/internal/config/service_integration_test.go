//go:build integration

package config_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

type evaluator struct {
	fail      bool
	calls     int
	lastInput jobs.EvaluationInput
}

func (e *evaluator) Evaluate(_ context.Context, input jobs.EvaluationInput) (jobs.Assessment, error) {
	e.calls++
	e.lastInput = input
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
		moderation_quarantine, moderation_config_pauses, moderation_config_versions, moderation_config_state, moderation_admin_audit`); err != nil {
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
	first, err := service.SaveDraft(
		ctx,
		"model",
		config.OutputModeJSONObject,
		config.InferenceParameters{},
		"admin",
		"Custom administrator policy",
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveDraft(
		ctx,
		"other-model",
		config.OutputModeJSONObject,
		config.InferenceParameters{},
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, first, "admin", nil, false); err != nil {
		t.Fatal(err)
	}
	activePrompt, err := service.ActiveConfiguration(ctx)
	if err != nil || activePrompt.SystemMessage != "Custom administrator policy" ||
		eval.lastInput.Config.SystemMessage != "Custom administrator policy" {
		t.Fatalf("custom prompt not persisted or used: %+v, %+v, %v", activePrompt, eval.lastInput.Config, err)
	}
	for _, invalid := range []string{"  ", strings.Repeat("x", 16385)} {
		if _, err := service.SaveDraft(
			ctx,
			"model",
			config.OutputModeJSONObject,
			config.InferenceParameters{},
			"admin",
			invalid,
		); err == nil {
			t.Fatal("invalid system message accepted")
		}
	}

	if err := service.Activate(ctx, second, "admin", nil, false); !errors.Is(err, config.ErrConfigurationConflict) {
		t.Fatalf("stale first activation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO moderation_config_pauses(version_id) VALUES ($1)`, first); err != nil {
		t.Fatal(err)
	}
	eval.fail = true
	if err := service.Activate(ctx, second, "admin", &first, true); err == nil {
		t.Fatal("failed provider validation activated draft")
	}
	active, err := service.ActiveConfiguration(ctx)
	if err != nil || active.VersionID != first {
		t.Fatalf("failed validation changed active config: %+v, %v", active, err)
	}
	var pauses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_config_pauses`).
		Scan(&pauses); err != nil ||
		pauses != 1 {
		t.Fatalf("failed validation released pause: %d, %v", pauses, err)
	}
	eval.fail = false
	if err := service.Activate(ctx, second, "admin", &first, false); err != nil {
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

	changedEndpoint := config.NewService(
		store,
		nil,
		eval,
		jobs.NewLimiter(1),
		config.Settings{BaseURL: "https://changed.example/v1", PolicyVersion: "review-text-v1"},
		time.Now,
	)
	calls := eval.calls
	if _, err := changedEndpoint.TestDraft(ctx, second, "benign"); err == nil {
		t.Fatal("old endpoint fingerprint passed validation")
	}
	if eval.calls != calls {
		t.Fatal("provider invoked before endpoint identity validation")
	}
	fresh, err := changedEndpoint.SaveDraft(
		ctx,
		"other-model",
		config.OutputModeJSONObject,
		config.InferenceParameters{},
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := changedEndpoint.Activate(ctx, fresh, "admin", &second, true); err != nil {
		t.Fatalf("new endpoint draft failed validation: %v", err)
	}
}
