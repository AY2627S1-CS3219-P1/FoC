// Package openaiadapter implements llmpolicy.CompletionClient over an
// OpenAI-compatible Chat Completions endpoint (for example the NUS soclaas
// proxy). It owns transport concerns only: URL and auth construction, disabling
// the SDK's automatic retries, enforcing the request deadline on the whole
// exchange including response reads, and mapping SDK/transport failures into
// the llmpolicy error classes. It never inspects or validates the assessment
// content; that is the evaluator's job.
package openaiadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"net/http"
	"net/url"
	"time"
)

const (
	// defaultBaseURL is the soclaas API entry point. It already carries the
	// single "/v1" segment the SDK appends "/chat/completions" to.
	defaultBaseURL = "https://soclaas-api.comp.nus.edu.sg/v1"
	// defaultTimeout bounds a request when neither the caller nor the evaluator
	// supplies a timeout.
	defaultTimeout = 90 * time.Second
)

// OpenAICompletionClient is an llmpolicy.CompletionClient backed by an
// OpenAI-compatible endpoint.
type OpenAICompletionClient struct {
	chatCompletions openai.ChatCompletionService
	baseURL         string
	timeout         time.Duration
}

var _ llmpolicy.CompletionClient = (*OpenAICompletionClient)(nil)

// Config holds explicit adapter configuration. There are no package globals;
// every field is supplied by the composition root.
type Config struct {
	// BaseURL is the provider entry point (CMS_LLM_BASE_URL). Empty selects the
	// default soclaas URL.
	BaseURL string
	// APIKey is the provider bearer key (CMS_LLM_API_KEY).
	APIKey string
	// Timeout bounds each request; zero selects the default.
	Timeout time.Duration
}

// New constructs the adapter. It validates the base URL and disables the SDK's
// automatic retries so the persistent runner owns retry scheduling.
func New(cfg Config) (*OpenAICompletionClient, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("openaiadapter: invalid base URL: %w", err)
	}
	if cfg.APIKey == "" {
		return nil, errors.New("openaiadapter: missing provider API key")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	transport := &http.Transport{
		ResponseHeaderTimeout: timeout, // guards slow response-header reads
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
	}

	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0), // the job engine owns retries, not the SDK
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)

	return &OpenAICompletionClient{
		chatCompletions: client.Chat.Completions,
		baseURL:         baseURL,
		timeout:         timeout,
	}, nil
}

// Complete issues one evaluation and maps the provider response. The request
// deadline (min of the caller's context deadline and the per-request timeout)
// is enforced on the whole exchange, including response reads.
func (c *OpenAICompletionClient) Complete(
	ctx context.Context,
	req *llmpolicy.CompletionRequest,
) (*llmpolicy.CompletionResponse, error) {
	params := buildParams(req)

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = c.timeout
	}

	opts := []option.RequestOption{option.WithRequestTimeout(timeout)}
	if advanced, err := advancedOptions(req.Parameters); err != nil {
		return nil, err
	} else {
		opts = append(opts, advanced...)
	}

	resp, err := c.chatCompletions.New(ctx, params, opts...)
	if err != nil {
		return nil, mapError(err)
	}
	return extractResponse(resp), nil
}
