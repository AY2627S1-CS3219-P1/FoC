package llmpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

// Evaluator applies one review assessment against the versioned policy using an
// OpenAI-compatible completion backend. It implements jobs.Evaluator. The
// completion client is injected; the Evaluator is safe for concurrent use.
type Evaluator struct {
	client CompletionClient
}

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
			Temperature:         cfg.Temperature,
			TopP:                cfg.TopP,
			PresencePenalty:     cfg.PresencePenalty,
			FrequencyPenalty:    cfg.FrequencyPenalty,
			MaxCompletionTokens: cfg.MaxCompletionTokens,
			MaxTokens:           cfg.MaxTokens,
			Stop:                cfg.Stop,
			Seed:                cfg.Seed,
			ReasoningEffort:     cfg.ReasoningEffort,
			Advanced:            cfg.Advanced,
		},
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
		return jobs.Assessment{}, &ErrRefusal{Reason: "provider declined to assess the content"}
	}
	if resp.FinishReason == "content_filter" {
		return jobs.Assessment{}, &ErrRefusal{Reason: "provider filtered the assessment"}
	}
	if resp.FinishReason != "stop" && resp.FinishReason != finishReasonLength {
		return jobs.Assessment{}, &ErrInvalidOutput{Detail: "unusable provider finish reason"}
	}
	if resp.FinishReason == finishReasonLength {
		return jobs.Assessment{}, &ErrInvalidOutput{
			Detail: "finish reason \"length\": response was truncated before completion",
		}
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return jobs.Assessment{}, ErrEmptyResponse
	}

	return parseAssessment(content)
}

// parseAssessment decodes exactly one JSON object and validates it against the
// policy schema, rejecting trailing data and unknown fields.
func parseAssessment(content string) (jobs.Assessment, error) {
	payload, err := decodeAssessment(content)
	if err != nil {
		return jobs.Assessment{}, err
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
			Detail: "decision must be approve or reject",
		}
	}
}

// decodeAssessment enforces presence, exact field names, types and uniqueness.
// encoding/json alone accepts missing/null fields and duplicate object keys.
func decodeAssessment(content string) (assessmentPayload, error) {
	var payload assessmentPayload
	invalid := func() (assessmentPayload, error) {
		return payload, &ErrInvalidOutput{Detail: "response does not match the assessment schema"}
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := make(map[string]json.RawMessage, 2)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok || (key != "decision" && key != "reason_codes") {
			return invalid()
		}
		if _, exists := fields[key]; exists {
			return invalid()
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return invalid()
		}
		fields[key] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return invalid()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid()
	}
	if len(fields) != 2 || string(fields["decision"]) == "null" {
		return invalid()
	}
	if err := json.Unmarshal(fields["decision"], &payload.Decision); err != nil {
		return invalid()
	}
	var codes []json.RawMessage
	if err := json.Unmarshal(fields["reason_codes"], &codes); err != nil || codes == nil {
		return invalid()
	}
	for _, raw := range codes {
		if string(raw) == "null" {
			return invalid()
		}
		var code string
		if err := json.Unmarshal(raw, &code); err != nil {
			return invalid()
		}
		payload.ReasonCodes = append(payload.ReasonCodes, code)
	}
	return payload, nil
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
			return nil, &ErrInvalidOutput{Detail: "unknown reason code"}
		}
		if _, dup := seen[code]; dup {
			return nil, &ErrInvalidOutput{Detail: "duplicate reason code"}
		}
		seen[code] = struct{}{}
	}
	return codes, nil
}

// assessmentPayload is the subset of the schema the evaluator decodes. An
// field set and JSON types are checked explicitly by decodeAssessment.
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
