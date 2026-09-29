package email

import "context"

type Email struct {
	To       []string
	Subject  string
	HTMLBody string
	TextBody string
}

type EmailSender interface {
	Send(context.Context, Email) error
}

// EmptyEmailSender discards messages until a delivery adapter is configured.
type EmptyEmailSender struct{}

func (EmptyEmailSender) Send(context.Context, Email) error {
	return nil
}
