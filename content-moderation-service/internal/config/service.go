package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/evaladapter"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/google/uuid"
)

// Settings carries the provider-facing wiring the service needs: the endpoint it
// lists models and runs draft tests against, the bearer key, and the code-owned
// policy version reported in setup status.
type Settings struct {
	BaseURL       string
	APIKey        string
	PolicyVersion string
}

// Service implements the admin configuration domain: drafts, activation, the
// active view and setup status. Drafts are validated and persisted but never
// activated; activation is an explicit, optimistically concurrent step.
type Service struct {
	store     *Store
	lister    ModelLister
	evaluator jobs.Evaluator
	limiter   *jobs.Limiter
	settings  Settings
	clock     func() time.Time
}

// NewService constructs the configuration service. The evaluator is only used by
// TestDraft; it may be nil when no provider is configured, in which case tests
// report that the service is not configured.
func NewService(
	store *Store,
	lister ModelLister,
	evaluator jobs.Evaluator,
	limiter *jobs.Limiter,
	settings Settings,
	clock func() time.Time,
) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		store:     store,
		lister:    lister,
		evaluator: evaluator,
		limiter:   limiter,
		settings:  settings,
		clock:     clock,
	}
}

// SetupStatus reports whether a configuration is active, whether a server-side
// credential exists, the active provider fingerprint and the compiled policy
// version. It never surfaces the credential itself.
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	status := SetupStatus{
		HasProviderCredential: s.settings.APIKey != "",
		PolicyVersion:         s.settings.PolicyVersion,
	}
	version, err := s.store.GetActiveConfig(ctx)
	if err != nil {
		if isErrNoActive(err) {
			return status, nil
		}
		return SetupStatus{}, err
	}
	status.HasActiveConfiguration = true
	status.ProviderFingerprint = version.ProviderFingerprint
	return status, nil
}

// ListModels returns the chat-capable models the provider reports. It fails when
// no model lister is configured (no base URL or key).
func (s *Service) ListModels(ctx context.Context) ([]Model, error) {
	if s.lister == nil {
		return nil, fmt.Errorf("config: provider model listing is not configured")
	}
	return s.lister.List(ctx)
}

// ActiveConfiguration returns the active configuration version, or
// ErrNoActiveConfiguration when none is activated.
func (s *Service) ActiveConfiguration(ctx context.Context) (Version, error) {
	version, err := s.store.GetActiveConfig(ctx)
	if err != nil {
		return Version{}, err
	}
	state, err := s.store.GetConfigState(ctx)
	if err != nil {
		return Version{}, err
	}
	return versionFromDB(version, state), nil
}

// SaveDraft validates and persists a configuration version without activating
// it. A validation failure leaves the active configuration untouched.
func (s *Service) SaveDraft(
	ctx context.Context,
	modelID, outputMode string,
	params InferenceParameters,
	createdBy string,
) (uuid.UUID, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return uuid.Nil, fmt.Errorf("config: model_id is required")
	}
	if outputMode != OutputModeJSONSchema && outputMode != OutputModeJSONObject {
		return uuid.Nil, fmt.Errorf("config: output_mode %q is not supported", outputMode)
	}
	if err := params.Validate(); err != nil {
		return uuid.Nil, err
	}
	encoded, err := params.encode()
	if err != nil {
		return uuid.Nil, err
	}
	fingerprint := providerFingerprint(s.settings.BaseURL, modelID)
	id, err := s.store.InsertConfigVersion(
		ctx,
		modelID,
		encoded,
		outputMode,
		s.settings.PolicyVersion,
		fingerprint,
		createdBy,
	)
	if err != nil {
		return uuid.Nil, err
	}
	_ = s.store.InsertAudit(ctx, "SAVE_DRAFT", &id, createdBy, map[string]any{"model_id": modelID})
	return id, nil
}

// TestDraft runs one bounded inference with a persisted draft against a benign
// sample, returning the sample verdict and duration. It shares the worker
// concurrency gate so a test cannot exhaust live assessment capacity.
func (s *Service) TestDraft(ctx context.Context, versionID uuid.UUID, sampleComment string) (TestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result := TestResult{ModelID: "", OutputMode: "", DurationMs: 0}
	if s.evaluator == nil {
		return result, fmt.Errorf("config: moderation provider is not configured; cannot test")
	}
	version, err := s.store.GetConfigVersionById(ctx, versionID)
	if err != nil {
		return result, err
	}
	var params InferenceParameters
	if err := params.Decode(version.Parameters); err != nil {
		return result, err
	}
	if err := params.Validate(); err != nil {
		result.ErrorClass = jobs.ErrorClassConfiguration
		return result, err
	}

	if err := s.limiter.Acquire(ctx); err != nil {
		return result, err
	}
	defer s.limiter.Release()

	start := s.clock()
	assessment, evalErr := s.evaluator.Evaluate(ctx, jobs.EvaluationInput{
		ModerationRequestID: "test",
		ReviewID:            "test",
		ReviewerID:          "test",
		ContentRevision:     1,
		Comment:             sampleComment,
		Config:              effectiveConfigFrom(version, params),
	})
	result.DurationMs = s.clock().Sub(start).Milliseconds()

	if evalErr != nil {
		result.ErrorClass = evaladapter.Classify(evalErr)
		return result, nil
	}
	switch assessment.Outcome {
	case jobs.OutcomeApproved:
		result.Decision = "approve"
	case jobs.OutcomeRejected:
		result.Decision = "reject"
		result.ReasonCodes = assessment.ReasonCodes
	default:
		result.ErrorClass = jobs.ErrorClassInvalidOutput
		return result, nil
	}
	result.ModelID = version.ModelID
	result.OutputMode = version.OutputMode
	return result, nil
}

// Activate activates draft versionID. expectedVersionID is the optimistic
// guard: nil means no active version is expected (first activation); otherwise
// the active version must equal it. The activation and its audit row commit
// together.
func (s *Service) Activate(
	ctx context.Context,
	versionID uuid.UUID,
	createdBy string,
	expectedVersionID *uuid.UUID,
	resumePausedJobs bool,
) error {
	// Validate the immutable draft through the same policy and concurrency gate
	// before changing the active pointer. Provider failure leaves it untouched.
	result, err := s.TestDraft(ctx, versionID, "The delivery arrived on time. Thank you.")
	if err != nil {
		return err
	}
	if result.ErrorClass != "" || result.Decision == "" {
		return fmt.Errorf("config: draft validation failed (%s)", result.ErrorClass)
	}
	return s.store.ActivateAndResume(ctx, versionID, createdBy, expectedVersionID, resumePausedJobs)
}

// Version is the admin-visible shape of an immutable configuration version.
type Version struct {
	VersionID           uuid.UUID
	ModelID             string
	Parameters          InferenceParameters
	OutputMode          string
	PolicyVersion       string
	ProviderFingerprint string
	CreatedBy           string
	CreatedAt           time.Time
	ActivatedBy         *string
	ActivatedAt         *time.Time
}

// SetupStatus is the read-only view returned by GetModerationSetupStatus.
type SetupStatus struct {
	HasActiveConfiguration bool
	HasProviderCredential  bool
	ProviderFingerprint    string
	PolicyVersion          string
}

// TestResult is the verdict a draft configuration produces for a sample.
type TestResult struct {
	Decision    string
	ReasonCodes []string
	ModelID     string
	OutputMode  string
	DurationMs  int64
	ErrorClass  string
}

func versionFromDB(version moderationdb.ModerationConfigVersion, state moderationdb.GetConfigStateRow) Version {
	var params InferenceParameters
	_ = params.Decode(version.Parameters)
	v := Version{
		VersionID:           version.VersionID,
		ModelID:             version.ModelID,
		Parameters:          params,
		OutputMode:          version.OutputMode,
		PolicyVersion:       version.PolicyVersion,
		ProviderFingerprint: version.ProviderFingerprint,
		CreatedBy:           version.CreatedBy,
		CreatedAt:           version.CreatedAt,
	}
	if state.ActivatedBy != nil {
		v.ActivatedBy = state.ActivatedBy
	}
	if state.ActivatedAt != nil {
		v.ActivatedAt = state.ActivatedAt
	}
	return v
}

func effectiveConfigFrom(
	version moderationdb.ModerationConfigVersion,
	params InferenceParameters,
) jobs.EffectiveConfig {
	return jobs.EffectiveConfig{
		VersionID:           version.VersionID.String(),
		ModelID:             version.ModelID,
		OutputMode:          jobs.OutputMode(version.OutputMode),
		Temperature:         params.Temperature,
		TopP:                params.TopP,
		PresencePenalty:     params.PresencePenalty,
		FrequencyPenalty:    params.FrequencyPenalty,
		MaxCompletionTokens: params.MaxCompletionTokens,
		MaxTokens:           params.MaxTokens,
		Stop:                params.Stop,
		Seed:                params.Seed,
		ReasoningEffort:     params.ReasoningEffort,
		Advanced:            params.Advanced,
	}
}

func providerFingerprint(baseURL, modelID string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + modelID))
	return hex.EncodeToString(sum[:])
}

func isErrNoActive(err error) bool {
	return err == ErrNoActiveConfiguration
}
