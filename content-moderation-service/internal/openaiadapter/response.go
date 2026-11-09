package openaiadapter

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	openai "github.com/openai/openai-go/v3"
)

// extractResponse copies only the service-neutral fields. Provider-supplied
// identities (completion IDs, request IDs) are never copied.
func extractResponse(resp *openai.ChatCompletion) *llmpolicy.CompletionResponse {
	if resp == nil || len(resp.Choices) == 0 {
		return &llmpolicy.CompletionResponse{}
	}
	choice := resp.Choices[0]
	return &llmpolicy.CompletionResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Refusal:      choice.Message.Refusal,
		Model:        resp.Model,
	}
}
