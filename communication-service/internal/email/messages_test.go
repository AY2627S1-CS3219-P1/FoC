package email

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

func TestMagicLinkEscapesTheLinkInHTML(t *testing.T) {
	e := MagicLink("alice@foc.local", `http://localhost:5173/login?token=a&b"c`, Login)
	if e.Subject != "Your FoC sign-in link" || !strings.Contains(e.HTMLBody, "token=a&amp;b&#34;c") ||
		!strings.Contains(e.TextBody, `token=a&b"c`) {
		t.Fatalf("email = %+v", e)
	}
}

func TestRejected(t *testing.T) {
	for err, want := range map[error]bool{
		fmt.Errorf("email: RCPT TO: %w", &textproto.Error{Code: 550, Msg: "no such user"}): true,
		&textproto.Error{Code: 451, Msg: "try again later"}:                                false,
		errors.New("email: dial: connection refused"):                                      false,
		fmt.Errorf("email: SES send: %w", &types.MessageRejected{}):                        true,
		&types.TooManyRequestsException{}:                                                  false,
		&types.SendingPausedException{}:                                                    false,
	} {
		if got := Rejected(err); got != want {
			t.Errorf("Rejected(%v) = %v, want %v", err, got, want)
		}
	}
}
