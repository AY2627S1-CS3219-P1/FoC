package review

import (
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"google.golang.org/protobuf/encoding/protojson"
)

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
