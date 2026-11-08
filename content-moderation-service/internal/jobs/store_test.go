// Package jobs_test contains unit tests for the moderation jobs engine.
package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	"github.com/google/uuid"
)

// TestDefaultPolicy tests the default policy.
func TestDefaultPolicy(t *testing.T) {
	policy := jobs.DefaultPolicy()
	if policy.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", policy.MaxAttempts)
	}
	if policy.MaxDelay == 0 {
		t.Error("MaxDelay is zero")
	}
}

// TestRetryable tests the Retryable function from llmpolicy.
func TestRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{"nil error", nil, false},
		{"ErrInvalidOutput", &llmpolicy.ErrInvalidOutput{Detail: "bad"}, false},
		{"ErrRefusal", &llmpolicy.ErrRefusal{Reason: "no"}, false},
		{"ErrContext", &llmpolicy.ErrContext{}, false},
		{"ErrConfiguration", &llmpolicy.ErrConfiguration{Detail: "bad config"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := llmpolicy.Retryable(tt.err)
			if got != tt.wantRetry {
				t.Errorf("Retryable(%v) = %v, want %v", tt.err, got, tt.wantRetry)
			}
		})
	}
}

// TestLimiter tests the limiter.
func TestLimiter(t *testing.T) {
	limiter := jobs.NewLimiter(2)

	if limiter == nil {
		t.Fatal("NewLimiter returned nil")
	}

	ctx := context.Background()
	if err := limiter.Acquire(ctx); err != nil {
		t.Errorf("Acquire failed: %v", err)
	}

	limiter.Release()

	// Note: we don't test Release on nil limiter as it causes a panic
}

// TestEvaluationInput tests the evaluation input structure.
func TestEvaluationInput(t *testing.T) {
	input := jobs.EvaluationInput{
		ModerationRequestID: "req-1",
		ReviewID:            "rev-1",
		ReviewerID:          "revi-1",
		ContentRevision:     1,
		Comment:             "test comment",
		Config: jobs.EffectiveConfig{
			ModelID: "test-model",
		},
	}

	if input.ModerationRequestID != "req-1" {
		t.Errorf("ModerationRequestID = %q, want req-1", input.ModerationRequestID)
	}
	if input.ReviewID != "rev-1" {
		t.Errorf("ReviewID = %q, want rev-1", input.ReviewID)
	}
}

// TestEffectiveConfig tests the effective config builder.
func TestEffectiveConfig(t *testing.T) {
	cfg := jobs.EffectiveConfig{
		VersionID:       "version-1",
		ModelID:         "test-model",
		OutputMode:      jobs.OutputModeJSONObject,
		ReasoningEffort: "high",
	}

	if cfg.ModelID != "test-model" {
		t.Errorf("ModelID = %q, want test-model", cfg.ModelID)
	}
	if cfg.OutputMode != jobs.OutputModeJSONObject {
		t.Errorf("OutputMode = %v, want %v", cfg.OutputMode, jobs.OutputModeJSONObject)
	}
}

// TestIngestionInput tests the ingestion input structure.
func TestIngestionInput(t *testing.T) {
	input := jobs.IngestionInput{
		SourceEventID:       uuid.New(),
		ModerationRequestID: uuid.New(),
		ReviewID:            uuid.New(),
		ReviewerID:          uuid.New(),
		ContentRevision:     1,
		Comment:             "test",
	}

	if input.SourceEventID == (uuid.UUID{}) {
		t.Error("SourceEventID is zero UUID")
	}
	if input.ReviewID == (uuid.UUID{}) {
		t.Error("ReviewID is zero UUID")
	}
}

// TestAssessmentOutput tests the assessment output structure.
func TestAssessmentOutput(t *testing.T) {
	assessment := jobs.Assessment{
		Outcome:       jobs.OutcomeApproved,
		ReasonCodes:   []string{},
		PolicyVersion: "review-text-v1",
	}

	if assessment.Outcome != jobs.OutcomeApproved {
		t.Errorf("Outcome = %v, want %v", assessment.Outcome, jobs.OutcomeApproved)
	}
	if len(assessment.ReasonCodes) != 0 {
		t.Errorf("ReasonCodes = %v, want empty", assessment.ReasonCodes)
	}
}

// TestOutputMode tests output mode constants.
func TestOutputMode(t *testing.T) {
	if jobs.OutputModeJSONObject == "" {
		t.Error("OutputModeJSONObject is empty")
	}
	if jobs.OutputModeJSONSchema == "" {
		t.Error("OutputModeJSONSchema is empty")
	}
}

// TestPolicy tests the policy structure.
func TestPolicy(t *testing.T) {
	p := jobs.Policy{
		MaxAttempts: 5,
		MaxDelay:    5 * time.Minute,
	}

	if p.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", p.MaxAttempts)
	}
	if p.MaxDelay == 0 {
		t.Error("MaxDelay is zero")
	}
}

// TestClassify tests error classification.
func TestClassify(t *testing.T) {
	// Note: classify is internal to jobs package
}

// TestLimiterAcquire tests limiter acquire behavior.
func TestLimiterAcquire(t *testing.T) {
	limiter := jobs.NewLimiter(1)

	ctx := context.Background()

	if err := limiter.Acquire(ctx); err != nil {
		t.Errorf("first Acquire failed: %v", err)
	}

	limiter.Release()
	if err := limiter.Acquire(ctx); err != nil {
		t.Errorf("second Acquire failed: %v", err)
	}

	limiter.Release()
}
