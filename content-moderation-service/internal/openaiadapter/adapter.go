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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
)

const (
	// defaultBaseURL is the soclaas API entry point. It already carries the
	// single "/v1" segment the SDK appends "/chat/completions" to.
	defaultBaseURL = "https://soclaas-api.comp.nus.edu.sg/v1"
	// defaultTimeout bounds a request when neither the caller nor the evaluator
	// supplies a timeout.
	defaultTimeout = 90 * time.Second
	// maxDiagnosticLength caps provider-derived text copied into errors.
	maxDiagnosticLength = 500
)

// reservedRequestFields are provider fields the Advanced parameter allowlist
// must never override. The adapter rejects any advanced entry keyed here so the
// model, prompts, output format, or request identity cannot be hijacked.
var reservedRequestFields = map[string]struct{}{
	"model":           {},
	"messages":        {},
	"system":          {},
	"user":            {},
	"response_format": {},
}

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

// buildParams assembles the completion request: a fresh single-turn
// conversation (system instruction + delimited review data), the configured
// model, and the strict structured-output format selected by the output mode.
func buildParams(req *llmpolicy.CompletionRequest) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(req.System),
			openai.UserMessage(req.User),
		},
		Model: openai.ChatModel(req.Model),
	}

	switch req.OutputMode {
	case "json_schema":
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        llmpolicy.SchemaName,
					Description: openai.Opt[string](llmpolicy.SchemaDescription),
					Schema:      json.RawMessage(req.SchemaJSON),
					Strict:      openai.Bool(true), // strict adherence; never silent fallback
				},
			},
		}
	default: // "json_object" and any future mode: local validation still applies
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		}
	}

	applyTypedParams(&params, req.Parameters)
	return params
}

// applyTypedParams sets optional inference parameters, omitting any that are
// unset so zero values are never sent.
func applyTypedParams(params *openai.ChatCompletionNewParams, paramsIn llmpolicy.CompletionParameters) {
	if paramsIn.Temperature != nil {
		params.Temperature = openai.Opt[float64](*paramsIn.Temperature)
	}
	if paramsIn.TopP != nil {
		params.TopP = openai.Opt[float64](*paramsIn.TopP)
	}
	if paramsIn.PresencePenalty != nil {
		params.PresencePenalty = openai.Opt[float64](*paramsIn.PresencePenalty)
	}
	if paramsIn.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Opt[float64](*paramsIn.FrequencyPenalty)
	}
	if paramsIn.MaxCompletionTokens != nil {
		params.MaxCompletionTokens = openai.Opt[int64](*paramsIn.MaxCompletionTokens)
	}
	if paramsIn.MaxTokens != nil {
		params.MaxTokens = openai.Opt[int64](*paramsIn.MaxTokens)
	}
	if paramsIn.Seed != nil {
		params.Seed = openai.Opt[int64](*paramsIn.Seed)
	}
	if paramsIn.ReasoningEffort != "" {
		params.ReasoningEffort = openai.ReasoningEffort(paramsIn.ReasoningEffort)
	}
	if len(paramsIn.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: paramsIn.Stop}
	}
}

// advancedOptions applies validated extra provider parameters via JSON merging.
// The service-owned allowlist excludes reserved fields; this guard is a
// defense in depth against a config that tries to override them.
func advancedOptions(paramsIn llmpolicy.CompletionParameters) ([]option.RequestOption, error) {
	if len(paramsIn.Advanced) == 0 {
		return nil, nil
	}
	opts := make([]option.RequestOption, 0, len(paramsIn.Advanced))
	for key, raw := range paramsIn.Advanced {
		if _, reserved := reservedRequestFields[key]; reserved {
			return nil, &llmpolicy.ErrConfiguration{Detail: fmt.Sprintf("advanced parameter %q is not permitted", key)}
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, &llmpolicy.ErrConfiguration{
				Detail: fmt.Sprintf("advanced parameter %q is not valid JSON: %s", key, bound(err.Error())),
			}
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	return opts, nil
}

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

// mapError converts an SDK or transport error into the appropriate llmpolicy
// error class, redacting credentials and bounding diagnostics.
func mapError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return mapTransportError(err)
	}
	switch {
	case apiErr.StatusCode == http.StatusTooManyRequests:
		return rateLimitedError(apiErr)
	case apiErr.StatusCode >= 500:
		// Server-side errors are transient and retryable.
		return &llmpolicy.ErrConnection{Detail: classifyMessage(apiErr)}
	case apiErr.StatusCode >= 400:
		// 4xx indicate a configuration or invalid request; not retried.
		return &llmpolicy.ErrConfiguration{Detail: classifyMessage(apiErr)}
	default:
		return &llmpolicy.ErrConnection{Detail: fmt.Sprintf("unexpected provider status %d", apiErr.StatusCode)}
	}
}

// mapTransportError handles non-API errors (connection, DNS, TLS, timeout) that
// the SDK does not wrap in *openai.Error.
func mapTransportError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &llmpolicy.ErrTimeout{Detail: "request deadline exceeded"}
	case errors.Is(err, context.Canceled):
		return &llmpolicy.ErrContext{}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &llmpolicy.ErrTimeout{Detail: "provider request timed out at the transport layer"}
		}
		return &llmpolicy.ErrConnection{Detail: bound(netErr.Error())}
	}
	return &llmpolicy.ErrConnection{Detail: bound(err.Error())}
}

// rateLimitedError builds an ErrRateLimited, honouring a Retry-After header when
// the provider advertises one.
func rateLimitedError(apiErr *openai.Error) *llmpolicy.ErrRateLimited {
	rlErr := &llmpolicy.ErrRateLimited{Detail: classifyMessage(apiErr)}
	if apiErr.Response != nil {
		if retryAfter, ok := parseRetryAfter(apiErr.Response.Header.Get("Retry-After")); ok {
			rlErr.RetryAfter = retryAfter
			rlErr.HasRetryAfter = true
		}
	}
	return rlErr
}

// parseRetryAfter parses a Retry-After value as either a delay in seconds or an
// HTTP date. It returns ok=false when the header is absent or unparsable.
func parseRetryAfter(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := parseIntSeconds(value); err == nil {
		if seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
	}
	if t, err := time.Parse(http.TimeFormat, value); err == nil {
		delay := time.Until(t)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

// parseIntSeconds parses a non-negative integer number of seconds.
func parseIntSeconds(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, err
	}
	return n, nil
}

// classifyMessage renders a short, sanitized provider error summary. It never
// leaks the API key or request headers.
func classifyMessage(apiErr *openai.Error) string {
	msg := apiErr.Message
	if apiErr.Type != "" {
		msg = apiErr.Type + ": " + msg
	}
	return bound(msg)
}

// bound shortens a provider-derived string so error text cannot grow without
// bound.
func bound(s string) string {
	const runeLimit = maxDiagnosticLength
	runes := []rune(s)
	if len(runes) > runeLimit {
		return string(runes[:runeLimit]) + "…"
	}
	return s
}
