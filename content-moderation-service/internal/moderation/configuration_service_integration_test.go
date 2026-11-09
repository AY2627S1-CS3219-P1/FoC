//go:build integration

package moderation_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
)

type evaluator struct {
	fail        bool
	calls       int
	lastRequest *moderation.CompletionRequest
}

func (e *evaluator) Complete(
	_ context.Context,
	request *moderation.CompletionRequest,
) (*moderation.CompletionResponse, error) {
	e.calls++
	e.lastRequest = request
	if e.fail {
		return nil, errors.New("provider unavailable")
	}
	return &moderation.CompletionResponse{
		Content:      `{"decision":"approve","reason_codes":[]}`,
		FinishReason: "stop",
	}, nil
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
	store := moderation.NewConfigurationStore(pool)
	if err := store.EnsureConfigState(ctx); err != nil {
		t.Fatal(err)
	}
	eval := &evaluator{}
	service := moderation.NewService(
		store, nil,
		nil,
		eval,
		moderation.NewInferenceLimiter(1),
		moderation.Settings{PolicyVersion: "review-text-v1"},
		time.Now,
	)
	first, err := service.SaveDraft(
		ctx,
		"model",
		moderation.OutputModeJSONObject,
		moderation.InferenceParameters{},
		"admin",
		"Custom administrator policy",
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveDraft(
		ctx,
		"other-model",
		moderation.OutputModeJSONObject,
		moderation.InferenceParameters{},
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, first, "admin", nil, false); err != nil {
		t.Fatal(err)
	}
	activePrompt, err := service.ActiveConfiguration(ctx)
	if err != nil || activePrompt.SystemMessage != "Custom administrator policy" ||
		eval.lastRequest.System != "Custom administrator policy" {
		t.Fatalf("custom prompt not persisted or used: %+v, %+v, %v", activePrompt, eval.lastRequest, err)
	}
	for _, invalid := range []string{"  ", strings.Repeat("x", 16385)} {
		if _, err := service.SaveDraft(
			ctx,
			"model",
			moderation.OutputModeJSONObject,
			moderation.InferenceParameters{},
			"admin",
			invalid,
		); err == nil {
			t.Fatal("invalid system message accepted")
		}
	}

	if err := service.Activate(ctx, second, "admin", nil, false); !errors.Is(err, moderation.ErrConfigurationConflict) {
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

	changedEndpoint := moderation.NewService(
		store, nil,
		nil,
		eval,
		moderation.NewInferenceLimiter(1),
		moderation.Settings{BaseURL: "https://changed.example/v1", PolicyVersion: "review-text-v1"},
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
		moderation.OutputModeJSONObject,
		moderation.InferenceParameters{},
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := changedEndpoint.Activate(ctx, fresh, "admin", &second, true); err != nil {
		t.Fatalf("new endpoint draft failed validation: %v", err)
	}
}
