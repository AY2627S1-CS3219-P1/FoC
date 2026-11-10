package moderation

// Service assesses review snapshots using immutable settings supplied at startup.
type Service struct {
	client  CompletionClient
	modelID string
}

func NewService(client CompletionClient, modelID string) *Service {
	return &Service{client: client, modelID: modelID}
}
