package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// These expectations follow the SESv2 SendEmail and STS web-identity wire
// contracts, plus the existing multipart email contract shared with SMTP.
func TestSESSenderWebIdentityEndToEnd(t *testing.T) {
	var stsCalls, sesCalls atomic.Int32
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stsCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("Action") != "AssumeRoleWithWebIdentity" || r.Form.Get("WebIdentityToken") != "synthetic-token" ||
			r.Form.Get("RoleArn") != "arn:aws:iam::123456789012:role/user-email" {
			t.Error("SDK did not use the configured web-identity role")
			http.Error(w, "invalid synthetic role request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprintf(
			w,
			`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIASYNTHETIC</AccessKeyId><SecretAccessKey>synthetic-secret</SecretAccessKey><SessionToken>synthetic-session</SessionToken><Expiration>%s</Expiration></Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		)
	}))
	t.Cleanup(sts.Close)
	ses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := sesCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v2/email/outbound-emails" {
			t.Errorf("SES request = %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "ASIASYNTHETIC/") ||
			!strings.Contains(r.Header.Get("Authorization"), "/ap-southeast-1/ses/aws4_request") ||
			r.Header.Get("X-Amz-Security-Token") != "synthetic-session" {
			t.Error("SES request was not signed with the temporary role credentials")
		}
		var request struct {
			FromEmailAddress string
			Destination      struct{ ToAddresses []string }
			Content          struct{ Raw struct{ Data []byte } }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.FromEmailAddress != "sender@example.test" ||
			strings.Join(request.Destination.ToAddresses, ",") != "one@example.test,two@example.test" {
			t.Error("SES sender or destination changed")
		}
		message, err := mail.ReadMessage(strings.NewReader(string(request.Content.Raw.Data)))
		if err != nil {
			t.Error(err)
			return
		}
		subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
		if err != nil || subject != "Your sign-in link ✓" ||
			message.Header.Get("From") != "\"FoC\" <sender@example.test>" {
			t.Errorf("MIME headers changed: subject=%q from=%q", subject, message.Header.Get("From"))
		}
		kind, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
		if err != nil || kind != "multipart/alternative" {
			t.Error("text/HTML MIME alternatives missing")
			return
		}
		parts := multipart.NewReader(message.Body, params["boundary"])
		for _, expected := range []struct{ kind, body string }{
			{"text/plain; charset=UTF-8", "Visit https://example.test/login?token=synthetic&purpose=login\n"},
			{"text/html; charset=UTF-8", `<a href="https://example.test/login?token=synthetic&amp;purpose=login">Continue</a>`},
		} {
			part, err := parts.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			body, err := io.ReadAll(part)
			if err != nil || part.Header.Get("Content-Type") != expected.kind ||
				strings.ReplaceAll(string(body), "\r\n", "\n") != expected.body {
				t.Error("MIME content changed across SES transport")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if call == 2 {
			w.Header().Set("X-Amzn-Errortype", "MessageRejected")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message":"synthetic rejection"}`)
			return
		}
		_, _ = io.WriteString(w, `{"MessageId":"synthetic-message"}`)
	}))
	t.Cleanup(ses.Close)
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_ENDPOINT_URL"} {
		t.Setenv(key, "")
	}
	directory := t.TempDir()
	empty := filepath.Join(directory, "empty")
	token := filepath.Join(directory, "token")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("synthetic-token"), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"AWS_CONFIG_FILE": empty, "AWS_SHARED_CREDENTIALS_FILE": empty,
		"AWS_EC2_METADATA_DISABLED": "true", "AWS_ROLE_ARN": "arn:aws:iam::123456789012:role/user-email",
		"AWS_ROLE_SESSION_NAME": "email-test", "AWS_WEB_IDENTITY_TOKEN_FILE": token,
		"AWS_ENDPOINT_URL_STS": sts.URL, "AWS_ENDPOINT_URL_SESV2": ses.URL,
	} {
		t.Setenv(key, value)
	}
	sender, err := NewSESSender(context.Background(), "ap-southeast-1", "FoC <sender@example.test>", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message := Email{
		To: []string{"one@example.test", "Two <two@example.test>"}, Subject: "Your sign-in link ✓",
		TextBody: "Visit https://example.test/login?token=synthetic&purpose=login\n",
		HTMLBody: `<a href="https://example.test/login?token=synthetic&amp;purpose=login">Continue</a>`,
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	var rejected *types.MessageRejected
	if err := sender.Send(context.Background(), message); !errors.As(err, &rejected) {
		t.Fatalf("SES rejection not preserved: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	if stsCalls.Load() != 1 || sesCalls.Load() != 2 {
		t.Fatalf("unexpected calls: STS=%d SES=%d", stsCalls.Load(), sesCalls.Load())
	}
	t.Log(
		"PASS: web-identity role -> temporary credentials -> signed SES requests; two MIME bodies, recipients, rejection, cancellation, and credential cache verified; no AWS calls or email delivery",
	)
}

type sesClientFunc func(context.Context, *sesv2.SendEmailInput) (*sesv2.SendEmailOutput, error)

func (f sesClientFunc) SendEmail(
	ctx context.Context,
	input *sesv2.SendEmailInput,
	_ ...func(*sesv2.Options),
) (*sesv2.SendEmailOutput, error) {
	return f(ctx, input)
}

func TestSESSenderValidationAndTimeout(t *testing.T) {
	for _, message := range []Email{
		{},
		{To: []string{"user@example.test\r\nBcc: injected@example.test"}},
		{To: []string{"user@example.test"}, Subject: "Hi\nInjected"},
		{To: []string{"not an address"}},
	} {
		sender := SESSender{
			From: "sender@example.test",
			Client: sesClientFunc(func(context.Context, *sesv2.SendEmailInput) (*sesv2.SendEmailOutput, error) {
				t.Error("invalid message reached SES")
				return nil, nil
			}),
		}
		if err := sender.Send(context.Background(), message); err == nil {
			t.Fatal("invalid email accepted")
		}
	}
	sender := SESSender{From: "sender@example.test", Timeout: 20 * time.Millisecond,
		Client: sesClientFunc(func(ctx context.Context, _ *sesv2.SendEmailInput) (*sesv2.SendEmailOutput, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}
	if err := sender.Send(
		context.Background(),
		Email{To: []string{"user@example.test"}},
	); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("timeout not preserved: %v", err)
	}
	if _, err := NewSESSender(context.Background(), "", "sender@example.test", time.Second); err == nil {
		t.Fatal("missing region accepted")
	}
	if _, err := NewSESSender(
		context.Background(),
		"ap-southeast-1",
		"sender@example.test\nInjected",
		time.Second,
	); !errors.Is(
		err,
		errHeaderInjection,
	) {
		t.Fatalf("sender injection accepted: %v", err)
	}
}
