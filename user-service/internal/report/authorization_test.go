package report

import (
	"errors"
	"net/http"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1/reportv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
)

func TestAuthorizeCaller(t *testing.T) {
	user := userdto.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)}}
	suspended := userdto.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuspendedUser)}}
	admin := userdto.Caller{Caller: auth.Caller{ID: "a", Role: string(tokenclaims.RoleAdmin), Admin: true}}

	for _, tc := range []struct {
		name        string
		caller      userdto.Caller
		procedure   string
		wantAllowed bool
	}{
		{"user may submit", user, reportv1connect.ReportServiceSubmitReportProcedure, true},
		{"admin may submit", admin, reportv1connect.ReportServiceSubmitReportProcedure, true},
		{"suspended cannot submit", suspended, reportv1connect.ReportServiceSubmitReportProcedure, false},
		// Suspended users keep access to the reports they filed.
		{"suspended may list own", suspended, reportv1connect.ReportServiceListMyReportsProcedure, true},
		{"suspended may get own", suspended, reportv1connect.ReportServiceGetReportProcedure, true},
		{"user cannot list all", user, reportv1connect.ReportServiceListReportsProcedure, false},
		{"admin may list all", admin, reportv1connect.ReportServiceListReportsProcedure, true},
		// The reported participant responds, even while suspended (U6.8.1).
		{"user may respond", user, reportv1connect.ReportServiceSubmitResponseProcedure, true},
		{"suspended may respond", suspended, reportv1connect.ReportServiceSubmitResponseProcedure, true},
		{"user cannot begin review", user, reportv1connect.ReportServiceBeginReviewProcedure, false},
		{"admin may begin review", admin, reportv1connect.ReportServiceBeginReviewProcedure, true},
		{"user cannot request response", user, reportv1connect.ReportServiceRequestResponseProcedure, false},
		{"admin may request response", admin, reportv1connect.ReportServiceRequestResponseProcedure, true},
		{"user cannot close", user, reportv1connect.ReportServiceCloseReportProcedure, false},
		{"suspended cannot close", suspended, reportv1connect.ReportServiceCloseReportProcedure, false},
		{"admin may close", admin, reportv1connect.ReportServiceCloseReportProcedure, true},
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
