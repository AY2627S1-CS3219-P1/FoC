package email

import (
	"errors"
	"html"
	"net/textproto"
)

// MagicLinkPurpose says whether a magic link signs in or finishes sign-up.
type MagicLinkPurpose int

const (
	Login MagicLinkPurpose = iota + 1
	Register
)

// MagicLink is the sign-in email User Service asks for.
func MagicLink(to, link string, purpose MagicLinkPurpose) Email {
	subject, intro := "Your FoC sign-in link", "Use this link to sign in to FoC:"
	if purpose == Register {
		subject, intro = "Finish creating your FoC account", "Use this link to finish creating your FoC account:"
	}
	const expiry = "The link works once and expires in 10 minutes. If you didn't ask for it, ignore this email."
	return Email{
		To:      []string{to},
		Subject: subject,
		HTMLBody: "<p>" + intro + "</p><p><a href=\"" + html.EscapeString(
			link,
		) + "\">Continue</a></p><p>" + expiry + "</p>",
		TextBody: intro + "\n" + link + "\n\n" + expiry,
	}
}

// OrderUpdate is an order notification for a user without Telegram.
func OrderUpdate(to, text string) Email {
	const footer = "Connect Telegram in your FoC profile to get every order update there."
	return Email{
		To:       []string{to},
		Subject:  "FoC order update",
		HTMLBody: "<p>" + html.EscapeString(text) + "</p><p>" + footer + "</p>",
		TextBody: text + "\n\n" + footer,
	}
}

// Rejected reports whether the mail server permanently refused the message,
// such as an unknown recipient, so sending it again would fail the same way.
func Rejected(err error) bool {
	var reply *textproto.Error
	return errors.As(err, &reply) && reply.Code >= 500
}
