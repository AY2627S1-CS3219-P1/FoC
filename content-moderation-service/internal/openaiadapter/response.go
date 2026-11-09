package openaiadapter

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
)

// extractResponse copies only the service-neutral fields. Provider-supplied
// identities (completion IDs, request IDs) are never copied.
func extractResponse(resp *openai.ChatCompletion) *moderation.CompletionResponse {
	if resp == nil || len(resp.Choices) == 0 {
		return &moderation.CompletionResponse{}
	}
	choice := resp.Choices[0]
	return &moderation.CompletionResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Refusal:      choice.Message.Refusal,
		Model:        resp.Model,
	}
}
