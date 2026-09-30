package errs_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

func TestMutationExternalErrors(t *testing.T) {
	cause := errors.New("private cause")
	for _, tc := range []struct {
		name    string
		plain   api.ExternalError
		wrapped api.ExternalError
		status  int
		code    connect.Code
	}{
		{"failed precondition", errs.NewFailedPreconditionError("missing reference"), errs.WrapFailedPreconditionError(cause, "missing reference"), http.StatusPreconditionFailed, connect.CodeFailedPrecondition},
		{"already exists", errs.NewAlreadyExistsError("key conflict"), errs.WrapAlreadyExistsError(cause, "key conflict"), http.StatusConflict, connect.CodeAlreadyExists},
		{"aborted", errs.NewAbortedError("stale revision"), errs.WrapAbortedError(cause, "stale revision"), http.StatusConflict, connect.CodeAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plain.Code() != tc.status || tc.plain.GetConnectCode() != tc.code || tc.plain.ErrorTrace() != tc.plain.Error() {
				t.Fatalf("plain error mapping: %v", tc.plain)
			}
			if tc.wrapped.Code() != tc.status || tc.wrapped.GetConnectCode() != tc.code || !errors.Is(tc.wrapped, cause) {
				t.Fatalf("wrapped error mapping: %v", tc.wrapped)
			}
			if tc.wrapped.ErrorTrace() != tc.wrapped.Error()+"\n"+cause.Error() {
				t.Fatalf("trace = %q", tc.wrapped.ErrorTrace())
			}
			got := api.ToConnectError(context.Background(), fmt.Errorf("operation: %w", tc.wrapped))
			if connect.CodeOf(got) != tc.code {
				t.Fatalf("connect code = %s, want %s", connect.CodeOf(got), tc.code)
			}
		})
	}
}
