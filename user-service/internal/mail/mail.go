// Package mail sends transactional email. SMTP works with Mailpit locally;
// swap in an SES implementation of Mailer for prod.
package mail

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

type Message struct {
	To      string
	Subject string
	Body    string // plain text
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTP sends via a plain SMTP relay (Mailpit: localhost:1025, no auth).
type SMTP struct {
	Addr     string
	From     string
	Username string // optional
	Password string // optional
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("mail: header injection rejected")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second) // NFR-17.1
	}
	conn, err := net.DialTimeout("tcp", s.Addr, time.Until(deadline))
	if err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	_ = conn.SetDeadline(deadline)
	host, _, _ := net.SplitHostPort(s.Addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("mail: client: %w", err)
	}
	defer c.Close()
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\n" +
		"To: " + m.To + "\r\n" +
		"Subject: " + m.Subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		strings.ReplaceAll(m.Body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Recorder captures messages in memory (tests).
type Recorder struct {
	mu   sync.Mutex
	Sent []Message
}

func (r *Recorder) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Sent = append(r.Sent, m)
	return nil
}

func (r *Recorder) Last() (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.Sent) == 0 {
		return Message{}, false
	}
	return r.Sent[len(r.Sent)-1], true
}
