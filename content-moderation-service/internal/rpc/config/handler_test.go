package config

import (
	"encoding/json"
	"testing"

	moderationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestOptionalParametersPreservePresenceThroughRPCMapping(t *testing.T) {
	for _, body := range []string{`{}`, `{"temperature":0,"topP":0,"presencePenalty":0,"frequencyPenalty":0}`} {
		message := &moderationv1.ModerationInferenceParameters{}
		if err := protojson.Unmarshal([]byte(body), message); err != nil {
			t.Fatal(err)
		}
		parameters, err := parametersFromProto(message)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip, err := protojson.Marshal(parametersProto(parameters))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(roundtrip, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"temperature", "topP", "presencePenalty", "frequencyPenalty"} {
			_, present := fields[key]
			if present != (body != `{}`) {
				t.Fatalf("%s presence lost: %s", key, roundtrip)
			}
		}
	}
}
