// Package config owns the admin configuration of the content moderation
// service's LLM evaluation: the immutable, versioned configuration objects,
// their validation, persistence and activation. Configuration never contains or
// returns provider credentials. The effective configuration is pinned onto a
// moderation job at claim time and read by the job engine unchanged.
package moderation

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// InferenceParameters is the validated subset of provider inference settings an
// admin may pin into a configuration version. Pointer fields are omitted from
// the stored JSON when nil so the adapter never sends an unset parameter as a
// zero value.
type InferenceParameters struct {
	Temperature         *float64
	TopP                *float64
	PresencePenalty     *float64
	FrequencyPenalty    *float64
	MaxCompletionTokens *int64
	MaxTokens           *int64
	Stop                []string
	Seed                *int64
	ReasoningEffort     string
	// Advanced holds validated provider parameters from the service-owned
	// allowlist. Keys are provider parameter names; values are raw JSON.
	Advanced map[string]string
}

// SupportedReasoningEfforts is the bounded set of reasoning-effort values the
// provider accepts. An unset value (empty) is valid; a set value must match.
var SupportedReasoningEfforts = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

// defaultTemperature etc. are not sent; they document accepted ranges. The
// validation below enforces the ranges, not these defaults.
var (
	temperatureMin = 0.0
	temperatureMax = 2.0
	topPMin        = 0.0
	topPMax        = 1.0
	penaltyMin     = -2.0
	penaltyMax     = 2.0
)

// advancedAllowlist is the service-owned set of provider parameters an admin may
// forward through advanced_parameters. It deliberately excludes every
// application-owned request field so the prompt, model, output format, or
// request identity can never be hijacked via this object.
var advancedAllowlist = map[string]struct{}{
	"top_k":              {},
	"min_p":              {},
	"repetition_penalty": {},
	"typical_p":          {},
}

// OutputMode selects how the evaluator requests structured output.

// Validate checks the ranges, mutual exclusivity and allowlist. It never mutates
// the receiver.
func (p *InferenceParameters) Validate() error {
	if err := validateFloat("temperature", p.Temperature, temperatureMin, temperatureMax); err != nil {
		return err
	}
	if err := validateFloat("top_p", p.TopP, topPMin, topPMax); err != nil {
		return err
	}
	if err := validateFloat("presence_penalty", p.PresencePenalty, penaltyMin, penaltyMax); err != nil {
		return err
	}
	if err := validateFloat("frequency_penalty", p.FrequencyPenalty, penaltyMin, penaltyMax); err != nil {
		return err
	}
	if p.MaxCompletionTokens != nil && *p.MaxCompletionTokens <= 0 {
		return fmt.Errorf("max_completion_tokens must be greater than 0")
	}
	if p.MaxTokens != nil && *p.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be greater than 0")
	}
	if p.MaxCompletionTokens != nil && p.MaxTokens != nil {
		return fmt.Errorf("max_completion_tokens and max_tokens are mutually exclusive")
	}
	if p.Seed != nil && *p.Seed < 0 {
		return fmt.Errorf("seed must be non-negative")
	}
	if err := validateStop(p.Stop); err != nil {
		return err
	}
	if err := validateReasoningEffort(p.ReasoningEffort); err != nil {
		return err
	}
	if err := validateAdvanced(p.Advanced); err != nil {
		return err
	}
	return nil
}

func validateFloat(name string, v *float64, min, max float64) error {
	if v == nil {
		return nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < min || *v > max {
		return fmt.Errorf("%s must be within [%v, %v]", name, min, max)
	}
	return nil
}

func validateStop(stop []string) error {
	if len(stop) > 4 {
		return fmt.Errorf("at most four stop sequences are supported")
	}
	seen := make(map[string]struct{}, len(stop))
	for _, s := range stop {
		if len(s) > 256 {
			return fmt.Errorf("stop sequence must not exceed 256 bytes")
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("stop sequence must not be empty")
		}
		if strings.Contains(s, "\n") {
			return fmt.Errorf("stop sequence must not contain a line break")
		}
		if _, dup := seen[s]; dup {
			return fmt.Errorf("stop sequence %q is repeated", s)
		}
		seen[s] = struct{}{}
	}
	return nil
}

func validateReasoningEffort(effort string) error {
	if effort == "" {
		return nil
	}
	if _, ok := SupportedReasoningEfforts[effort]; !ok {
		return fmt.Errorf("reasoning_effort %q is not supported", effort)
	}
	return nil
}

func validateAdvanced(advanced map[string]string) error {
	if len(advanced) == 0 {
		return nil
	}
	names := make([]string, 0, len(advanced))
	for key, raw := range advanced {
		if _, allowed := advancedAllowlist[key]; !allowed {
			return fmt.Errorf("advanced parameter %q is not in the allowlist", key)
		}
		if len(raw) > 64 || strings.TrimSpace(raw) == "null" {
			return fmt.Errorf("advanced parameter %q must be a bounded number", key)
		}
		var number float64
		if err := json.Unmarshal([]byte(raw), &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("advanced parameter %q must be a finite number", key)
		}
		switch key {
		case "top_k":
			if number < 1 || number > 1000 || math.Trunc(number) != number {
				return fmt.Errorf("top_k must be an integer from 1 to 1000")
			}
		case "min_p", "typical_p":
			if number < 0 || number > 1 {
				return fmt.Errorf("%s must be between 0 and 1", key)
			}
		case "repetition_penalty":
			if number <= 0 || number > 2 {
				return fmt.Errorf("repetition_penalty must be greater than 0 and at most 2")
			}
		}
		names = append(names, key)
	}
	sort.Strings(names)
	return nil
}

// encode renders the parameters to the JSON layout the job engine decodes,
// omitting any field that is not set.
func (p *InferenceParameters) encode() ([]byte, error) {
	// The tag layout here is the contract shared with effectiveConfig; it
	// must stay in lock-step with that struct.
	type layout struct {
		Temperature         *float64          `json:"temperature,omitempty"`
		TopP                *float64          `json:"top_p,omitempty"`
		PresencePenalty     *float64          `json:"presence_penalty,omitempty"`
		FrequencyPenalty    *float64          `json:"frequency_penalty,omitempty"`
		MaxCompletionTokens *int64            `json:"max_completion_tokens,omitempty"`
		MaxTokens           *int64            `json:"max_tokens,omitempty"`
		Stop                []string          `json:"stop,omitempty"`
		Seed                *int64            `json:"seed,omitempty"`
		ReasoningEffort     *string           `json:"reasoning_effort,omitempty"`
		Advanced            map[string]string `json:"advanced_parameters,omitempty"`
	}
	return json.Marshal(layout{
		Temperature:         p.Temperature,
		TopP:                p.TopP,
		PresencePenalty:     p.PresencePenalty,
		FrequencyPenalty:    p.FrequencyPenalty,
		MaxCompletionTokens: p.MaxCompletionTokens,
		MaxTokens:           p.MaxTokens,
		Stop:                p.Stop,
		Seed:                p.Seed,
		ReasoningEffort:     ptrString(p.ReasoningEffort),
		Advanced:            p.Advanced,
	})
}

// Decode parses stored parameters. A missing field is left nil.
func (p *InferenceParameters) Decode(data []byte) error {
	type layout struct {
		Temperature         *float64          `json:"temperature"`
		TopP                *float64          `json:"top_p"`
		PresencePenalty     *float64          `json:"presence_penalty"`
		FrequencyPenalty    *float64          `json:"frequency_penalty"`
		MaxCompletionTokens *int64            `json:"max_completion_tokens"`
		MaxTokens           *int64            `json:"max_tokens"`
		Stop                []string          `json:"stop"`
		Seed                *int64            `json:"seed"`
		ReasoningEffort     *string           `json:"reasoning_effort"`
		Advanced            map[string]string `json:"advanced_parameters"`
	}
	var l layout
	if err := json.Unmarshal(data, &l); err != nil {
		return fmt.Errorf("decode parameters: %w", err)
	}
	*p = InferenceParameters{
		Temperature:         l.Temperature,
		TopP:                l.TopP,
		PresencePenalty:     l.PresencePenalty,
		FrequencyPenalty:    l.FrequencyPenalty,
		MaxCompletionTokens: l.MaxCompletionTokens,
		MaxTokens:           l.MaxTokens,
		Stop:                l.Stop,
		Seed:                l.Seed,
		ReasoningEffort:     configDerefString(l.ReasoningEffort),
		Advanced:            l.Advanced,
	}
	return nil
}

func ptrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func configDerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
