package moderation

import "context"

// Model is one chat-capable model reported by the provider listing. The model
// ID carries no capability guarantee; the admin still pins a model into a
// configuration.
type Model struct {
	ModelID string
	OwnedBy string
}

// ModelLister discovers the chat-capable models the provider exposes. The only
// implementation talks to the provider's authenticated model listing.
type ModelLister interface {
	List(ctx context.Context) ([]Model, error)
}
