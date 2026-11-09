package config

import (
	"math"
	"strings"
	"testing"
)

func TestAdvancedParameterProfileRejectsInvalidTypesAndBounds(t *testing.T) {
	for key, values := range map[string][]string{
		"top_k": {`null`, `true`, `"40"`, `{}`, `[]`, `0`, `1.5`, `1001`, strings.Repeat(" ", 65) + `40`},
		"min_p": {`-0.01`, `1.01`}, "typical_p": {`-1`, `2`},
		"repetition_penalty": {`0`, `-1`, `2.1`}, "messages": {`[]`}, "stream": {`true`},
	} {
		for _, raw := range values {
			t.Run(key+"/"+raw, func(t *testing.T) {
				p := InferenceParameters{Advanced: map[string]string{key: raw}}
				if err := p.Validate(); err == nil {
					t.Fatalf("accepted %s=%s", key, raw)
				}
			})
		}
	}
	valid := InferenceParameters{
		Advanced: map[string]string{"top_k": "40", "min_p": "0", "typical_p": "1", "repetition_penalty": "1.2"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNonFiniteAndUnboundedParametersAreRejected(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		p := InferenceParameters{Temperature: &value}
		if err := p.Validate(); err == nil {
			t.Fatal("accepted non-finite temperature")
		}
	}
	for _, stop := range [][]string{{"1", "2", "3", "4", "5"}, {strings.Repeat("x", 257)}} {
		p := InferenceParameters{Stop: stop}
		if err := p.Validate(); err == nil {
			t.Fatal("accepted unbounded stop sequences")
		}
	}
}
