// Package validate normalises and checks user-supplied fields. Each helper
// records a field message in apperr.Fields and returns the cleaned value.
package validate

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"user-service/internal/apperr"
)

const (
	MaxDisplayName = 50
	MaxDescription = 500
	MaxReason      = 2000
)

var (
	telegramRe = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)
	phoneRe    = regexp.MustCompile(`^\+?[0-9]{8,15}$`)
	domainRe   = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}$`)
)

// Email lower-cases and trims; rejects "Name <a@b>" forms and hosts without a dot.
func Email(f apperr.Fields, field, raw string) string {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		f.Add(field, "is required")
		return ""
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(EmailDomain(e), ".") {
		f.Add(field, "must be a valid email address, e.g. name@u.nus.edu")
		return ""
	}
	return e
}

// EmailDomain returns the part after the last '@'.
func EmailDomain(email string) string {
	return email[strings.LastIndex(email, "@")+1:]
}

func Domain(f apperr.Fields, field, raw string) string {
	d := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if !domainRe.MatchString(d) {
		f.Add(field, "must be a domain like u.nus.edu (no @ or protocol)")
		return ""
	}
	return d
}

func DisplayName(f apperr.Fields, field, raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		f.Add(field, "is required")
	case utf8.RuneCountInString(s) > MaxDisplayName:
		f.Add(field, "must be at most 50 characters")
	}
	return s
}

func Description(f apperr.Fields, field, raw string) string {
	s := strings.TrimSpace(raw)
	if utf8.RuneCountInString(s) > MaxDescription {
		f.Add(field, "must be at most 500 characters")
	}
	return s
}

// Telegram strips a leading @. nil or "" => nil (cleared).
func Telegram(f apperr.Fields, field string, raw *string) *string {
	if raw == nil {
		return nil
	}
	h := strings.TrimPrefix(strings.TrimSpace(*raw), "@")
	if h == "" {
		return nil
	}
	if !telegramRe.MatchString(h) {
		f.Add(field, "must be 5-32 letters, digits or underscores (leading @ optional)")
		return nil
	}
	return &h
}

// Phone strips spaces/dashes. nil or "" => nil (cleared).
func Phone(f apperr.Fields, field string, raw *string) *string {
	if raw == nil {
		return nil
	}
	p := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(*raw))
	if p == "" {
		return nil
	}
	if !phoneRe.MatchString(p) {
		f.Add(field, "must be 8-15 digits, optionally starting with +")
		return nil
	}
	return &p
}

// Reason trims; required controls whether blank is an error. Returns nil if blank.
func Reason(f apperr.Fields, field string, raw *string, required bool) *string {
	s := ""
	if raw != nil {
		s = strings.TrimSpace(*raw)
	}
	switch {
	case s == "" && required:
		f.Add(field, "is required")
		return nil
	case s == "":
		return nil
	case utf8.RuneCountInString(s) > MaxReason:
		f.Add(field, "must be at most 2000 characters")
	}
	return &s
}
