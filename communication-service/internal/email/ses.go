package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SESClient is the SES operation used by this delivery adapter.
type SESClient interface {
	SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// SESSender sends the same MIME messages as SMTP through the SES HTTPS API.
// The default AWS credential chain supports Kubernetes web-identity roles.
type SESSender struct {
	Client SESClient
	From   string
	// Timeout bounds delivery when ctx has no deadline. Zero means 5s.
	Timeout time.Duration
}

func NewSESSender(ctx context.Context, region, from string, timeout time.Duration) (*SESSender, error) {
	if region == "" {
		return nil, errors.New("email: SES region is required")
	}
	if strings.ContainsAny(from, "\r\n") {
		return nil, errHeaderInjection
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("email: parse sender: %w", err)
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("email: load AWS configuration: %w", err)
	}
	return &SESSender{Client: sesv2.NewFromConfig(cfg), From: from, Timeout: timeout}, nil
}

func (s SESSender) Send(ctx context.Context, e Email) error {
	if len(e.To) == 0 {
		return errors.New("email: no recipients")
	}
	for _, value := range append([]string{s.From, e.Subject}, e.To...) {
		if strings.ContainsAny(value, "\r\n") {
			return errHeaderInjection
		}
	}
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("email: parse sender: %w", err)
	}
	recipients := make([]string, 0, len(e.To))
	for _, value := range e.To {
		recipient, err := mail.ParseAddress(value)
		if err != nil {
			return fmt.Errorf("email: parse recipient: %w", err)
		}
		recipients = append(recipients, recipient.Address)
	}
	message, err := buildMessage(from, e, time.Now())
	if err != nil {
		return err
	}
	if s.Client == nil {
		return errors.New("email: SES client is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		timeout := s.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_, err = s.Client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(from.Address),
		Destination:      &types.Destination{ToAddresses: recipients},
		Content:          &types.EmailContent{Raw: &types.RawMessage{Data: message}},
	})
	if err != nil {
		return fmt.Errorf("email: SES send: %w", err)
	}
	return nil
}
