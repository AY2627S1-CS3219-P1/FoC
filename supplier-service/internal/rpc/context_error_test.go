package rpc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
)

type contextErrorLog struct {
	sync.Mutex
	bytes.Buffer
}

func (l *contextErrorLog) Write(p []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	return l.Buffer.Write(p)
}

func (l *contextErrorLog) text() string {
	l.Lock()
	defer l.Unlock()
	return l.Buffer.String()
}

func TestHandlerContextErrorMapping(t *testing.T) {
	for _, row := range []struct {
		name   string
		err    error
		code   connect.Code
		logged bool
	}{
		{"cancelled", context.Canceled, connect.CodeCanceled, false},
		{"wrapped cancellation", fmt.Errorf("private database detail: %w", context.Canceled), connect.CodeCanceled, false},
		{"deadline", context.DeadlineExceeded, connect.CodeDeadlineExceeded, false},
		{"wrapped deadline", fmt.Errorf("private database detail: %w", context.DeadlineExceeded), connect.CodeDeadlineExceeded, false},
		{"unexpected failure", errors.New("private database detail"), connect.CodeInternal, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var logs contextErrorLog
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			_, client := fastClients(t, &applicationStub{err: row.err})
			_, err := client.SubmitLocationAdditionRequest(context.Background(), fastRequest(&pb.SubmitLocationAdditionRequestRequest{Proposal: fastProposal(), IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}, "user"))
			if connect.CodeOf(err) != row.code {
				t.Fatalf("context error mapping: got %v, want %v", err, row.code)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("client error leaked private detail: %v", err)
			}
			if got := strings.Contains(logs.text(), "Supplier workflow failed"); got != row.logged {
				t.Fatalf("unexpected server fault logging: %q", logs.text())
			}
		})
	}
}
