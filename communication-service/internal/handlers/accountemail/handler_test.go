package accountemail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
)

type fakeSender struct {
	sent []email.Email
	err  error
}

func (f *fakeSender) Send(_ context.Context, e email.Email) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, e)
	return nil
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func request(purpose communicationv1.MagicLinkPurpose) *connect.Request[communicationv1.SendMagicLinkRequest] {
	return connect.NewRequest(&communicationv1.SendMagicLinkRequest{
		Email: "alice@foc.local", Link: "http://localhost:5173/register?token=abc", Purpose: purpose,
	})
}

func TestSendMagicLinkWritesThePurposesEmail(t *testing.T) {
	cases := map[communicationv1.MagicLinkPurpose]string{
		communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_LOGIN:    "Your FoC sign-in link",
		communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_REGISTER: "Finish creating your FoC account",
	}
	for purpose, subject := range cases {
		sender := &fakeSender{}
		h := &Handler{Sender: sender, Log: discard}
		if _, err := h.SendMagicLink(context.Background(), request(purpose)); err != nil {
			t.Fatal(err)
		}
		if len(sender.sent) != 1 {
			t.Fatalf("sent = %+v", sender.sent)
		}
		got := sender.sent[0]
		if got.To[0] != "alice@foc.local" || got.Subject != subject ||
			!strings.Contains(got.TextBody, "http://localhost:5173/register?token=abc") {
			t.Fatalf("email = %+v", got)
		}
	}
}

func TestSendMagicLinkReportsFailuresAsUnavailable(t *testing.T) {
	for name, h := range map[string]*Handler{
		"not configured": {Log: discard},
		"send failed":    {Sender: &fakeSender{err: errors.New("dial tcp: refused")}, Log: discard},
	} {
		_, err := h.SendMagicLink(
			context.Background(),
			request(communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_LOGIN),
		)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
