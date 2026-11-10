package appeal

import (
	"errors"
	"net/http"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/actor"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/appeal/v1/appealv1connect"
)

func TestAuthorizeCaller(t *testing.T) {
	user := actor.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)}}
	suspended := actor.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuspendedUser)}}
	admin := actor.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleAdmin), Admin: true}}

	for _, tc := range []struct {
		name        string
		caller      actor.Caller
		procedure   string
		wantAllowed bool
	}{
		{"user may appeal", user, appealv1connect.AppealServiceSubmitAppealProcedure, true},
		// A suspended user may appeal, including their suspension (U6.8.1).
		{"suspended may appeal", suspended, appealv1connect.AppealServiceSubmitAppealProcedure, true},
		{"suspended may list own", suspended, appealv1connect.AppealServiceListMyAppealsProcedure, true},
		{"suspended may get own", suspended, appealv1connect.AppealServiceGetAppealProcedure, true},
		{"suspended may respond", suspended, appealv1connect.AppealServiceSubmitResponseProcedure, true},
		{"user cannot list all", user, appealv1connect.AppealServiceListAppealsProcedure, false},
		{"admin may list all", admin, appealv1connect.AppealServiceListAppealsProcedure, true},
		{"user cannot begin review", user, appealv1connect.AppealServiceBeginReviewProcedure, false},
		{"admin may begin review", admin, appealv1connect.AppealServiceBeginReviewProcedure, true},
		{"user cannot request response", user, appealv1connect.AppealServiceRequestResponseProcedure, false},
		{"admin may request response", admin, appealv1connect.AppealServiceRequestResponseProcedure, true},
		{"suspended cannot close", suspended, appealv1connect.AppealServiceCloseAppealProcedure, false},
		{"admin may close", admin, appealv1connect.AppealServiceCloseAppealProcedure, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizeCaller(tc.caller, tc.procedure)
			if tc.wantAllowed {
				if err != nil {
					t.Fatalf("expected allowed, got %v", err)
				}
				return
			}

			var external api.ExternalError
			if !errors.As(err, &external) || external.Code() != http.StatusForbidden {
				t.Fatalf("expected forbidden, got %v", err)
			}
		})
	}
}
