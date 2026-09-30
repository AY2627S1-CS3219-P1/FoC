package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

type connectTestError struct {
	status int
	code   connect.Code
}

func (e connectTestError) Error() string                { return "visible message" }
func (e connectTestError) ErrorTrace() string           { return "visible message" }
func (e connectTestError) Code() int                    { return e.status }
func (e connectTestError) GetConnectCode() connect.Code { return e.code }

func TestToConnectErrorUsesWrappedExternalError(t *testing.T) {
	cases := []struct {
		status int
		code   connect.Code
	}{
		{http.StatusBadRequest, connect.CodeInvalidArgument},
		{http.StatusUnauthorized, connect.CodeUnauthenticated},
		{http.StatusForbidden, connect.CodePermissionDenied},
		{http.StatusNotFound, connect.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			err := ToConnectError(context.Background(), fmt.Errorf("context: %w", connectTestError{status: tc.status, code: tc.code}))
			if got := connect.CodeOf(err); got != tc.code {
				t.Fatalf("code = %v, want %v", got, tc.code)
			}
			if !strings.Contains(err.Error(), "visible message") || strings.Contains(err.Error(), "context:") {
				t.Fatalf("unexpected public message: %v", err)
			}
		})
	}
}

func TestToConnectErrorHidesUnknownError(t *testing.T) {
	err := ToConnectError(context.Background(), errors.New("private database detail"))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("code = %v, want internal", got)
	}
	if strings.Contains(err.Error(), "private database detail") {
		t.Fatalf("internal detail exposed: %v", err)
	}
}
