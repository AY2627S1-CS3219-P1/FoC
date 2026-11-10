package openaiadapter

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
)

func buildParams(req *moderation.CompletionRequest) openai.ChatCompletionNewParams {
	return openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(req.System),
			openai.UserMessage(req.User),
		},
		Model: openai.ChatModel(req.Model),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		},
	}
}
