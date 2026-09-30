package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

const defaultSMTPTimeout = 5 * time.Second

var errHeaderInjection = errors.New("email: header contains a line break")

// SMTPSender delivers messages through an SMTP relay. Locally this is Mailpit
// (no auth, no TLS). STARTTLS is used whenever the server offers it, and
// credentials are sent only when Username is set.
type SMTPSender struct {
	Addr     string
	From     string
	Username string
	Password string
	// Timeout bounds the whole exchange when ctx has no deadline. Zero means 5s.
	Timeout time.Duration
}

func (s SMTPSender) Send(ctx context.Context, e Email) error {
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
	message, err := buildMessage(from, e, time.Now())
	if err != nil {
		return err
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		timeout := s.Timeout
		if timeout <= 0 {
			timeout = defaultSMTPTimeout
		}
		deadline = time.Now().Add(timeout)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", s.Addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("email: set deadline: %w", err)
	}
	// Close the connection early if ctx is cancelled before the deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("email: parse address: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("email: greeting: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	for _, recipient := range e.To {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("email: RCPT TO: %w", err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: end body: %w", err)
	}
	return client.Quit()
}

// buildMessage renders e as an RFC 5322 message with CRLF line endings. A
// message with both bodies is multipart/alternative with the text part first.
func buildMessage(from *mail.Address, e Email, now time.Time) ([]byte, error) {
	messageID, err := newMessageID(from.Address)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeHeader := func(key, value string) {
		buf.WriteString(key + ": " + value + "\r\n")
	}
	writeHeader("From", from.String())
	writeHeader("To", strings.Join(e.To, ", "))
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", e.Subject))
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("Message-ID", messageID)
	writeHeader("MIME-Version", "1.0")

	switch {
	case e.TextBody != "" && e.HTMLBody != "":
		parts := multipart.NewWriter(&buf)
		writeHeader("Content-Type", mime.FormatMediaType("multipart/alternative",
			map[string]string{"boundary": parts.Boundary()}))
		buf.WriteString("\r\n")
		for _, part := range []struct{ contentType, body string }{
			{"text/plain", e.TextBody},
			{"text/html", e.HTMLBody},
		} {
			w, err := parts.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {part.contentType + "; charset=UTF-8"},
				"Content-Transfer-Encoding": {"quoted-printable"},
			})
			if err != nil {
				return nil, err
			}
			if err := writeQuotedPrintable(w, part.body); err != nil {
				return nil, err
			}
		}
		if err := parts.Close(); err != nil {
			return nil, err
		}
	case e.HTMLBody != "":
		writeHeader("Content-Type", "text/html; charset=UTF-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, e.HTMLBody); err != nil {
			return nil, err
		}
	default:
		writeHeader("Content-Type", "text/plain; charset=UTF-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, e.TextBody); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func writeQuotedPrintable(w interface{ Write([]byte) (int, error) }, body string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}

func newMessageID(fromAddress string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("email: message ID: %w", err)
	}
	domain := "localhost"
	if at := strings.LastIndexByte(fromAddress, '@'); at >= 0 && at < len(fromAddress)-1 {
		domain = fromAddress[at+1:]
	}
	return "<" + hex.EncodeToString(random) + "@" + domain + ">", nil
}
