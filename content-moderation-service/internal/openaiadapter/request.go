package openaiadapter

import (
	"encoding/json"
	"fmt"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// reservedRequestFields are provider fields the Advanced parameter allowlist
// must never override. The adapter rejects any advanced entry keyed here so the
// model, prompts, output format, or request identity cannot be hijacked.
var reservedRequestFields = map[string]struct{}{
	"model":           {},
	"messages":        {},
	"system":          {},
	"user":            {},
	"response_format": {},
}

// buildParams assembles the completion request: a fresh single-turn
// conversation (system instruction + delimited review data), the configured
// model, and the strict structured-output format selected by the output mode.
func buildParams(req *llmpolicy.CompletionRequest) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(req.System),
			openai.UserMessage(req.User),
		},
		Model: openai.ChatModel(req.Model),
	}

	switch req.OutputMode {
	case "json_schema":
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        llmpolicy.SchemaName,
					Description: openai.Opt[string](llmpolicy.SchemaDescription),
					Schema:      json.RawMessage(req.SchemaJSON),
					Strict:      openai.Bool(true), // strict adherence; never silent fallback
				},
			},
		}
	default: // "json_object" and any future mode: local validation still applies
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		}
	}

	applyTypedParams(&params, req.Parameters)
	return params
}

// applyTypedParams sets optional inference parameters, omitting any that are
// unset so zero values are never sent.
func applyTypedParams(params *openai.ChatCompletionNewParams, paramsIn llmpolicy.CompletionParameters) {
	if paramsIn.Temperature != nil {
		params.Temperature = openai.Opt[float64](*paramsIn.Temperature)
	}
	if paramsIn.TopP != nil {
		params.TopP = openai.Opt[float64](*paramsIn.TopP)
	}
	if paramsIn.PresencePenalty != nil {
		params.PresencePenalty = openai.Opt[float64](*paramsIn.PresencePenalty)
	}
	if paramsIn.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Opt[float64](*paramsIn.FrequencyPenalty)
	}
	if paramsIn.MaxCompletionTokens != nil {
		params.MaxCompletionTokens = openai.Opt[int64](*paramsIn.MaxCompletionTokens)
	}
	if paramsIn.MaxTokens != nil {
		params.MaxTokens = openai.Opt[int64](*paramsIn.MaxTokens)
	}
	if paramsIn.Seed != nil {
		params.Seed = openai.Opt[int64](*paramsIn.Seed)
	}
	if paramsIn.ReasoningEffort != "" {
		params.ReasoningEffort = openai.ReasoningEffort(paramsIn.ReasoningEffort)
	}
	if len(paramsIn.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: paramsIn.Stop}
	}
}

// advancedOptions applies validated extra provider parameters via JSON merging.
// The service-owned allowlist excludes reserved fields; this guard is a
// defense in depth against a config that tries to override them.
func advancedOptions(paramsIn llmpolicy.CompletionParameters) ([]option.RequestOption, error) {
	if len(paramsIn.Advanced) == 0 {
		return nil, nil
	}
	opts := make([]option.RequestOption, 0, len(paramsIn.Advanced))
	for key, raw := range paramsIn.Advanced {
		if _, reserved := reservedRequestFields[key]; reserved {
			return nil, &llmpolicy.ErrConfiguration{Detail: fmt.Sprintf("advanced parameter %q is not permitted", key)}
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, &llmpolicy.ErrConfiguration{
				Detail: fmt.Sprintf("advanced parameter %q is not valid JSON: %s", key, "provider transport failed"),
			}
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	return opts, nil
}
