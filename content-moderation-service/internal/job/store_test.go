package job_test

import (
	"context"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/google/uuid"
)

func TestDefaultPolicy(t *testing.T) {
	policy := moderationjob.DefaultRetrySettings()
	if policy.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", policy.MaxAttempts)
	}
	if policy.MaxDelay == 0 {
		t.Error("MaxDelay is zero")
	}
}

func TestLimiter(t *testing.T) {
	limiter := moderation.NewInferenceLimiter(2)

	if limiter == nil {
		t.Fatal("NewInferenceLimiter returned nil")
	}

	ctx := context.Background()
	if err := limiter.Acquire(ctx); err != nil {
		t.Errorf("Acquire failed: %v", err)
	}

	limiter.Release()
}

func TestEvaluationInput(t *testing.T) {
	input := moderation.ReviewInput{
		ModerationRequestID: "req-1",
		ReviewID:            "rev-1",
		ReviewerID:          "revi-1",
		ContentRevision:     1,
		Comment:             "test comment",
	}

	if input.ModerationRequestID != "req-1" {
		t.Errorf("ModerationRequestID = %q, want req-1", input.ModerationRequestID)
	}
	if input.ReviewID != "rev-1" {
		t.Errorf("ReviewID = %q, want rev-1", input.ReviewID)
	}
}

func TestIngestionInput(t *testing.T) {
	input := moderationjob.IngestionInput{
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

func TestAssessmentOutput(t *testing.T) {
	assessment := moderation.ReviewAssessment{
		Outcome:       moderation.OutcomeApproved,
		ReasonCodes:   []string{},
		PolicyVersion: "review-text-v1",
	}

	if assessment.Outcome != moderation.OutcomeApproved {
		t.Errorf("Outcome = %v, want %v", assessment.Outcome, moderation.OutcomeApproved)
	}
	if len(assessment.ReasonCodes) != 0 {
		t.Errorf("ReasonCodes = %v, want empty", assessment.ReasonCodes)
	}
}

func TestOutputMode(t *testing.T) {
	if moderation.OutputModeJSONObject == "" {
		t.Error("OutputModeJSONObject is empty")
	}
	if moderation.OutputModeJSONSchema == "" {
		t.Error("OutputModeJSONSchema is empty")
	}
}

func TestPolicy(t *testing.T) {
	p := moderationjob.RetrySettings{
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

func TestLimiterAcquire(t *testing.T) {
	limiter := moderation.NewInferenceLimiter(1)

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
