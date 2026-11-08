package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

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

// httpModelLister lists models from an OpenAI-compatible GET {baseURL}/models
// endpoint. It carries no credentials in responses and never exposes the key.
type httpModelLister struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewModelLister builds a provider model lister. It fails if the base URL or the
// API key is empty, so a caller with no credential simply does not construct one.
func NewModelLister(baseURL, apiKey string, timeout time.Duration) (*httpModelLister, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("config: provider base URL is required to list models")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("config: provider API key is required to list models")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &httpModelLister{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

// List performs one authenticated GET {baseURL}/models and maps the OpenAI-style
// listing to Model values. A non-2xx response is an error, not an empty list.
func (l *httpModelLister) List(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("config: build model listing request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("config: list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("config: model listing returned status %d", resp.StatusCode)
	}

	var list struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("config: decode model listing: %w", err)
	}

	models := make([]Model, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, Model{ModelID: m.ID, OwnedBy: m.OwnedBy})
	}
	return models, nil
}
