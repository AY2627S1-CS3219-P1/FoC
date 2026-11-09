package review

import (
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSummaryConfigurationPause(t *testing.T) {
	for _, tc := range []struct {
		status string
		class  string
		paused bool
	}{
		{status: "retry_wait", class: "configuration", paused: true},
		{status: "retry_wait", class: "connection", paused: false},
		{status: "errored", class: "configuration", paused: false},
	} {
		t.Run(tc.status+"/"+tc.class, func(t *testing.T) {
			summary := summarize(
				moderation.JobView{
					ModerationJob:       moderationdb.ModerationJob{Status: tc.status, ErrorClass: &tc.class},
					ConfigurationPaused: tc.paused,
				},
			)
			if summary.ConfigurationPaused != tc.paused {
				t.Fatalf("configuration_paused = %v, want %v", summary.ConfigurationPaused, tc.paused)
			}
		})
	}
}

func TestAttemptProjectionOmitsDiagnostic(t *testing.T) {
	diagnostic := "private-provider-diagnostic"
	class := "connection"
	view := attemptOf(moderationdb.ModerationAttempt{Detail: &diagnostic, ErrorClass: &class})
	encoded, err := protojson.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), diagnostic) || view.ErrorClass != class {
		t.Fatalf("expected stable class without diagnostic: %s", encoded)
	}
}
