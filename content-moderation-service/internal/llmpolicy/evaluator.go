package llmpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

// defaultRequestTimeout bounds a single evaluation when the completion port is
// called without an explicit timeout. It is enforced by the adapter on the
// whole exchange, including response reads.
const defaultRequestTimeout = 90 * time.Second

// maxDiagnosticLength caps any provider-derived text (refusals, error details)
// copied into an error so raw diagnostics cannot grow without bound.
const maxDiagnosticLength = 500

// Evaluator applies one review assessment against the versioned policy using an
// OpenAI-compatible completion backend. It implements jobs.Evaluator. The
// completion client is injected; the Evaluator is safe for concurrent use.
type Evaluator struct {
	client CompletionClient
}

// NewEvaluator constructs the LLM-backed evaluator. The client is the only
// dependency; the composition root injects an OpenAI-compatible adapter.
func NewEvaluator(client CompletionClient) *Evaluator {
	return &Evaluator{client: client}
}

// Evaluate assesses one review comment against the versioned policy. It builds
// a fresh single-turn request from the pinned config, calls the injected
// completion client, validates the response locally, and maps the outcome. An
// error return is always a technical failure (one of the llmpolicy error
// classes); no verdict is inferred from it.
func (e *Evaluator) Evaluate(ctx context.Context, input jobs.EvaluationInput) (jobs.Assessment, error) {
	req := buildRequest(input)
	resp, err := e.client.Complete(ctx, req)
	if err != nil {
		return jobs.Assessment{}, err
	}
	return evaluateResponse(resp)
}

// buildRequest maps the pinned effective config into a completion request with
// a fresh single-turn conversation: the trusted system prompt plus the
// complete, delimited review as the user message.
func buildRequest(input jobs.EvaluationInput) *CompletionRequest {
	cfg := input.Config
	return &CompletionRequest{
		Model:      cfg.ModelID,
		System:     systemMessage(),
		User:       userMessage(input.Comment),
		OutputMode: string(cfg.OutputMode),
		SchemaJSON: SchemaJSON,
		Parameters: CompletionParameters{
			Temperature:       cfg.Temperature,
			TopP:              cfg.TopP,
			PresencePenalty:   cfg.PresencePenalty,
			FrequencyPenalty:  cfg.FrequencyPenalty,
			MaxCompletionTokens: cfg.MaxCompletionTokens,
			MaxTokens:         cfg.MaxTokens,
			Stop:              cfg.Stop,
			Seed:              cfg.Seed,
			ReasoningEffort:   cfg.ReasoningEffort,
			Advanced:          cfg.Advanced,
		},
		Timeout: defaultRequestTimeout,
	}
}

// evaluateResponse turns a provider response into an assessment, enforcing the
// output contract locally. A refusal, empty response, or truncated generation
// yields a technical error, never a verdict.
func evaluateResponse(resp *CompletionResponse) (jobs.Assessment, error) {
	if resp == nil {
		return jobs.Assessment{}, ErrEmptyResponse
	}
	if reason := strings.TrimSpace(resp.Refusal); reason != "" {
		return jobs.Assessment{}, &ErrRefusal{Reason: bound(reason)}
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return jobs.Assessment{}, ErrEmptyResponse
	}
	if resp.FinishReason == finishReasonLength {
		return jobs.Assessment{}, &ErrInvalidOutput{
			Detail: "finish reason \"length\": response was truncated before completion",
		}
	}
	return parseAssessment(content)
}

// parseAssessment decodes exactly one JSON object and validates it against the
// policy schema, rejecting trailing data and unknown fields.
func parseAssessment(content string) (jobs.Assessment, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()

	var payload assessmentPayload
	if err := decoder.Decode(&payload); err != nil {
		return jobs.Assessment{}, &ErrInvalidOutput{Detail: "response is not a valid JSON object: " + bound(err.Error())}
	}
	// Reject trailing garbage or a second JSON object after the verdict.
	if _, err := decoder.Token(); err != io.EOF {
		return jobs.Assessment{}, &ErrInvalidOutput{Detail: "response contains more than one JSON object"}
	}

	switch payload.Decision {
	case "approve":
		if len(payload.ReasonCodes) != 0 {
			return jobs.Assessment{}, &ErrInvalidOutput{
				Detail: fmt.Sprintf("approve decision must not include reason codes, got %d", len(payload.ReasonCodes)),
			}
		}
		return jobs.Assessment{Outcome: jobs.OutcomeApproved, ReasonCodes: nil, PolicyVersion: PolicyVersion}, nil

	case "reject":
		codes, err := validateReasonCodes(payload.ReasonCodes)
		if err != nil {
			return jobs.Assessment{}, err
		}
		return jobs.Assessment{Outcome: jobs.OutcomeRejected, ReasonCodes: codes, PolicyVersion: PolicyVersion}, nil

	default:
		return jobs.Assessment{}, &ErrInvalidOutput{
			Detail: fmt.Sprintf("invalid decision %q: want \"approve\" or \"reject\"", payload.Decision),
		}
	}
}

// validateReasonCodes checks the reject codes against the known set: bounded
// size, all known, no duplicates. The order of the input is preserved. A reject
// verdict requires at least one code, so an empty set is invalid.
func validateReasonCodes(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return nil, &ErrInvalidOutput{Detail: "reject decision requires at least one reason code"}
	}
	if len(codes) > maxReasonCodes {
		return nil, &ErrInvalidOutput{
			Detail: fmt.Sprintf("too many reason codes: got %d, max %d", len(codes), maxReasonCodes),
		}
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if _, ok := knownReasonCode[code]; !ok {
			return nil, &ErrInvalidOutput{Detail: fmt.Sprintf("unknown reason code %q", code)}
		}
		if _, dup := seen[code]; dup {
			return nil, &ErrInvalidOutput{Detail: fmt.Sprintf("duplicate reason code %q", code)}
		}
		seen[code] = struct{}{}
	}
	return codes, nil
}

// bound shortens a provider-derived string so error text cannot grow without
// bound, while never surfacing the evaluated content itself.
func bound(s string) string {
	const runeLimit = maxDiagnosticLength
	runes := []rune(s)
	if len(runes) > runeLimit {
		return string(runes[:runeLimit]) + "…"
	}
	return s
}

// assessmentPayload is the subset of the schema the evaluator decodes. An
// unknown field is rejected by the decoder's DisallowUnknownFields setting.
type assessmentPayload struct {
	Decision    string   `json:"decision"`
	ReasonCodes []string `json:"reason_codes"`
}

// knownReasonCode indexes the policy's rejection codes.
var knownReasonCode = map[string]struct{}{
	jobs.ReasonHarassment:    {},
	jobs.ReasonHate:          {},
	jobs.ReasonThreat:        {},
	jobs.ReasonSexualContent: {},
	jobs.ReasonSpam:          {},
}

var _ jobs.Evaluator = (*Evaluator)(nil)
