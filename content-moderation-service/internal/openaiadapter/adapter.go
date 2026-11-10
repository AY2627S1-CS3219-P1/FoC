// Package openaiadapter translates provider transport and errors; moderation validates content.
package openaiadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const (
	defaultBaseURL = "https://soclaas-api.comp.nus.edu.sg/v1"
	defaultTimeout = 90 * time.Second
)

type OpenAICompletionClient struct {
	chatCompletions openai.ChatCompletionService
	baseURL         string
	timeout         time.Duration
}

var _ moderation.CompletionClient = (*OpenAICompletionClient)(nil)

type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// Disable SDK retries so durable job execution owns attempt accounting.
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

// Enforce the earliest caller/request deadline through response reads.
func (c *OpenAICompletionClient) Complete(
	ctx context.Context,
	req *moderation.CompletionRequest,
) (*moderation.CompletionResponse, error) {
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
