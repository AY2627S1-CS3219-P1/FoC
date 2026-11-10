package openaiadapter

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
)

// Omit provider request/completion IDs from the service response.
func extractResponse(resp *openai.ChatCompletion) *moderation.CompletionResponse {
	if resp == nil || len(resp.Choices) == 0 {
		return &moderation.CompletionResponse{}
	}
	choice := resp.Choices[0]
	return &moderation.CompletionResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Refusal:      choice.Message.Refusal,
	}
}
