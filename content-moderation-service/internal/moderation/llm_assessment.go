package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Assessment errors are technical failures; never infer a verdict from them.
func (e *Service) AssessReview(ctx context.Context, input ReviewInput) (ReviewAssessment, error) {
	if e.client == nil {
		return ReviewAssessment{}, &ErrConfiguration{Detail: "moderation provider is not configured"}
	}
	req := buildRequest(input, e.modelID)
	resp, err := e.client.Complete(ctx, req)
	if err != nil {
		return ReviewAssessment{}, err
	}
	return evaluateResponse(resp)
}

func buildRequest(input ReviewInput, modelID string) *CompletionRequest {
	return &CompletionRequest{
		Model:      modelID,
		System:     systemMessage(),
		User:       userMessage(input.Comment),
		OutputMode: OutputModeJSONObject,
		SchemaJSON: SchemaJSON,
	}

}

// Refusal, empty content and truncation are technical failures, even if JSON looks valid.
func evaluateResponse(resp *CompletionResponse) (ReviewAssessment, error) {
	if resp == nil {
		return ReviewAssessment{}, ErrEmptyResponse
	}
	if reason := strings.TrimSpace(resp.Refusal); reason != "" {
		return ReviewAssessment{}, &ErrRefusal{Reason: "provider declined to assess the content"}
	}
	if resp.FinishReason == "content_filter" {
		return ReviewAssessment{}, &ErrRefusal{Reason: "provider filtered the assessment"}
	}
	if resp.FinishReason != "stop" && resp.FinishReason != finishReasonLength {
		return ReviewAssessment{}, &ErrInvalidOutput{Detail: "unusable provider finish reason"}
	}
	if resp.FinishReason == finishReasonLength {
		return ReviewAssessment{}, &ErrInvalidOutput{
			Detail: "finish reason \"length\": response was truncated before completion",
		}
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return ReviewAssessment{}, ErrEmptyResponse
	}

	return parseAssessment(content)
}

func parseAssessment(content string) (ReviewAssessment, error) {
	payload, err := decodeAssessment(content)
	if err != nil {
		return ReviewAssessment{}, err
	}

	switch payload.Decision {
	case "approve":
		if len(payload.ReasonCodes) != 0 {
			return ReviewAssessment{}, &ErrInvalidOutput{
				Detail: fmt.Sprintf("approve decision must not include reason codes, got %d", len(payload.ReasonCodes)),
			}
		}
		return ReviewAssessment{Outcome: OutcomeApproved, ReasonCodes: nil, PolicyVersion: PolicyVersion}, nil

	case "reject":
		codes, err := validateReasonCodes(payload.ReasonCodes)
		if err != nil {
			return ReviewAssessment{}, err
		}
		return ReviewAssessment{Outcome: OutcomeRejected, ReasonCodes: codes, PolicyVersion: PolicyVersion}, nil

	default:
		return ReviewAssessment{}, &ErrInvalidOutput{
			Detail: "decision must be approve or reject",
		}
	}
}

// encoding/json alone accepts missing/null fields and duplicate keys.
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

type assessmentPayload struct {
	Decision    string   `json:"decision"`
	ReasonCodes []string `json:"reason_codes"`
}

var knownReasonCode = map[string]struct{}{
	ReasonHarassment:    {},
	ReasonHate:          {},
	ReasonThreat:        {},
	ReasonSexualContent: {},
	ReasonSpam:          {},
}
