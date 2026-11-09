// Package config maps the moderation.v1 admin contract to the config domain:
// setup status, model listing, drafts, draft tests, activation and the active
// view. Configuration never contains or returns provider credentials, and every
// RPC is admin-gated by the router's RequireAdmin interceptor.
package config

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	moderationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	service *config.Service
}

func NewHandler(service *config.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetModerationSetupStatus(
	ctx context.Context,
	req *connect.Request[moderationv1.GetModerationSetupStatusRequest],
) (*connect.Response[moderationv1.GetModerationSetupStatusResponse], error) {
	status, err := h.service.SetupStatus(ctx)
	if err != nil {
		return nil, err
	}
	resp := connect.NewResponse[moderationv1.GetModerationSetupStatusResponse](
		&moderationv1.GetModerationSetupStatusResponse{
			HasActiveConfiguration: status.HasActiveConfiguration,
			HasProviderCredential:  status.HasProviderCredential,
			ProviderFingerprint:    status.ProviderFingerprint,
			PolicyVersion:          status.PolicyVersion,
		},
	)
	return resp, nil
}

func (h *Handler) ListAvailableModels(
	ctx context.Context,
	req *connect.Request[moderationv1.ListAvailableModelsRequest],
) (*connect.Response[moderationv1.ListAvailableModelsResponse], error) {
	models, err := h.service.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	resp := &moderationv1.ListAvailableModelsResponse{}
	for _, m := range models {
		resp.Models = append(resp.Models, &moderationv1.ModerationModel{
			ModelId: m.ModelID,
			OwnedBy: m.OwnedBy,
		})
	}
	return connect.NewResponse(resp), nil
}

// GetActiveModerationConfiguration returns the active configuration version, or
// an empty configuration when none has been activated yet.
func (h *Handler) GetActiveModerationConfiguration(
	ctx context.Context,
	req *connect.Request[moderationv1.GetActiveModerationConfigurationRequest],
) (*connect.Response[moderationv1.GetActiveModerationConfigurationResponse], error) {
	active, err := h.service.ActiveConfiguration(ctx)
	if err != nil {
		if errors.Is(err, config.ErrNoActiveConfiguration) {
			resp := connect.NewResponse[moderationv1.GetActiveModerationConfigurationResponse](
				&moderationv1.GetActiveModerationConfigurationResponse{},
			)
			return resp, nil
		}
		return nil, err
	}
	resp := connect.NewResponse[moderationv1.GetActiveModerationConfigurationResponse](
		&moderationv1.GetActiveModerationConfigurationResponse{
			Configuration: configurationProto(active),
		},
	)
	return resp, nil
}

func (h *Handler) SaveModerationConfigurationDraft(
	ctx context.Context,
	req *connect.Request[moderationv1.SaveModerationConfigurationDraftRequest],
) (*connect.Response[moderationv1.SaveModerationConfigurationDraftResponse], error) {
	caller, ok := callerID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errs.NewUnauthorizedError("unauthenticated"))
	}

	params, err := parametersFromProto(req.Msg.GetParameters())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError(err.Error()))
	}
	versionID, err := h.service.SaveDraft(
		ctx,
		req.Msg.GetModelId(),
		outputModeString(req.Msg.GetOutputMode()),
		params,
		caller,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError(err.Error()))
	}
	resp := connect.NewResponse[moderationv1.SaveModerationConfigurationDraftResponse](
		&moderationv1.SaveModerationConfigurationDraftResponse{
			DraftVersionId: versionID.String(),
		},
	)
	return resp, nil
}

// TestModerationConfigurationDraft runs one bounded test inference for the draft.
func (h *Handler) TestModerationConfigurationDraft(
	ctx context.Context,
	req *connect.Request[moderationv1.TestModerationConfigurationDraftRequest],
) (*connect.Response[moderationv1.TestModerationConfigurationDraftResponse], error) {
	versionID, err := uuid.Parse(req.Msg.GetDraftVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid draft_version_id"))
	}
	result, err := h.service.TestDraft(ctx, versionID, req.Msg.GetSampleComment())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New(err.Error()))
	}
	resp := connect.NewResponse[moderationv1.TestModerationConfigurationDraftResponse](
		&moderationv1.TestModerationConfigurationDraftResponse{
			Decision:    result.Decision,
			ReasonCodes: result.ReasonCodes,
			ModelId:     result.ModelID,
			OutputMode:  result.OutputMode,
			DurationMs:  result.DurationMs,
			ErrorClass:  result.ErrorClass,
		},
	)
	return resp, nil
}

// ActivateModerationConfiguration activates a draft with optimistic concurrency.
func (h *Handler) ActivateModerationConfiguration(
	ctx context.Context,
	req *connect.Request[moderationv1.ActivateModerationConfigurationRequest],
) (*connect.Response[moderationv1.ActivateModerationConfigurationResponse], error) {
	caller, ok := callerID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errs.NewUnauthorizedError("unauthenticated"))
	}
	versionID, err := uuid.Parse(req.Msg.GetDraftVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid draft_version_id"))
	}

	var expected *uuid.UUID
	if requested := req.Msg.GetExpectedActiveVersionId(); requested != "" {
		parsed, perr := uuid.Parse(requested)
		if perr != nil {
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				errs.NewBadRequestError("invalid expected_active_version_id"),
			)
		}
		expected = &parsed
	}

	if err := h.service.Activate(ctx, versionID, caller, expected, req.Msg.GetResumePausedJobs()); err != nil {
		if errors.Is(err, config.ErrConfigurationConflict) {
			return nil, connect.NewError(
				connect.CodeFailedPrecondition,
				errs.NewFailedPreconditionError("active configuration changed; activation rejected"),
			)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New(err.Error()))
	}

	active, err := h.service.ActiveConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	resp := connect.NewResponse[moderationv1.ActivateModerationConfigurationResponse](
		&moderationv1.ActivateModerationConfigurationResponse{
			Configuration: configurationProto(active),
		},
	)
	return resp, nil
}

func callerID(ctx context.Context) (string, bool) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok || caller.ID == "" {
		return "", false
	}
	return caller.ID, true
}

func configurationProto(v config.Version) *moderationv1.ModerationConfiguration {
	proto := &moderationv1.ModerationConfiguration{
		VersionId:           v.VersionID.String(),
		ModelId:             v.ModelID,
		Parameters:          parametersProto(v.Parameters),
		OutputMode:          outputModeProto(v.OutputMode),
		PolicyVersion:       v.PolicyVersion,
		ProviderFingerprint: v.ProviderFingerprint,
		CreatedBy:           v.CreatedBy,
		CreatedAt:           timestamppb.New(v.CreatedAt),
	}
	if v.ActivatedBy != nil {
		proto.ActivatedBy = *v.ActivatedBy
	}
	if v.ActivatedAt != nil {
		proto.ActivatedAt = timestamppb.New(*v.ActivatedAt)
	}
	return proto
}

func parametersProto(p config.InferenceParameters) *moderationv1.ModerationInferenceParameters {
	params := &moderationv1.ModerationInferenceParameters{
		AdvancedParameters: p.Advanced,
	}
	if p.Temperature != nil {
		params.Temperature = p.Temperature
	}
	if p.TopP != nil {
		params.TopP = p.TopP
	}
	if p.PresencePenalty != nil {
		params.PresencePenalty = p.PresencePenalty
	}
	if p.FrequencyPenalty != nil {
		params.FrequencyPenalty = p.FrequencyPenalty
	}
	params.MaxCompletionTokens = p.MaxCompletionTokens
	params.MaxTokens = p.MaxTokens
	params.Stop = p.Stop
	params.Seed = p.Seed
	params.ReasoningEffort = ptrString(p.ReasoningEffort)
	return params
}

func parametersFromProto(p *moderationv1.ModerationInferenceParameters) (config.InferenceParameters, error) {
	if p == nil {
		return config.InferenceParameters{}, nil
	}
	return config.InferenceParameters{
		Temperature:         p.Temperature,
		TopP:                p.TopP,
		PresencePenalty:     p.PresencePenalty,
		FrequencyPenalty:    p.FrequencyPenalty,
		MaxCompletionTokens: p.MaxCompletionTokens,
		MaxTokens:           p.MaxTokens,
		Stop:                p.Stop,
		Seed:                p.Seed,
		ReasoningEffort:     derefString(p.ReasoningEffort),
		Advanced:            p.AdvancedParameters,
	}, nil
}

func outputModeProto(mode string) moderationv1.ModerationOutputMode {
	switch mode {
	case config.OutputModeJSONSchema:
		return moderationv1.ModerationOutputMode_MODERATION_OUTPUT_MODE_JSON_SCHEMA
	case config.OutputModeJSONObject:
		return moderationv1.ModerationOutputMode_MODERATION_OUTPUT_MODE_JSON_OBJECT
	default:
		return moderationv1.ModerationOutputMode_MODERATION_OUTPUT_MODE_UNSPECIFIED
	}
}

func outputModeString(mode moderationv1.ModerationOutputMode) string {
	switch mode {
	case moderationv1.ModerationOutputMode_MODERATION_OUTPUT_MODE_JSON_SCHEMA:
		return config.OutputModeJSONSchema
	case moderationv1.ModerationOutputMode_MODERATION_OUTPUT_MODE_JSON_OBJECT:
		return config.OutputModeJSONObject
	default:
		return ""
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
