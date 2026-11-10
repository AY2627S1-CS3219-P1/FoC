package openaiadapter

import (
	"encoding/json"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Advanced parameters must not override model, prompt or output fields.
var reservedRequestFields = map[string]struct{}{
	"model":           {},
	"messages":        {},
	"system":          {},
	"user":            {},
	"response_format": {},
}

func buildParams(req *moderation.CompletionRequest) openai.ChatCompletionNewParams {
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
					Name:        moderation.SchemaName,
					Description: openai.Opt[string](moderation.SchemaDescription),
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

func applyTypedParams(params *openai.ChatCompletionNewParams, paramsIn moderation.CompletionParameters) {
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

// Guard the allowlist again at the adapter boundary.
func advancedOptions(paramsIn moderation.CompletionParameters) ([]option.RequestOption, error) {
	if len(paramsIn.Advanced) == 0 {
		return nil, nil
	}
	opts := make([]option.RequestOption, 0, len(paramsIn.Advanced))
	for key, raw := range paramsIn.Advanced {
		if _, reserved := reservedRequestFields[key]; reserved {
			return nil, &moderation.ErrConfiguration{Detail: fmt.Sprintf("advanced parameter %q is not permitted", key)}
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, &moderation.ErrConfiguration{
				Detail: fmt.Sprintf("advanced parameter %q is not valid JSON: %s", key, "provider transport failed"),
			}
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	return opts, nil
}
