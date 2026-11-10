package moderation

type Service struct {
	client  CompletionClient
	modelID string
}

func NewService(client CompletionClient, modelID string) *Service {
	return &Service{client: client, modelID: modelID}
}
