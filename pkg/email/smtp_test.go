package email

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildMessageMultipartAlternative(t *testing.T) {
	from := &mail.Address{Name: "FoC", Address: "no-reply@foc.local"}
	raw, err := buildMessage(from, Email{
		To:       []string{"a@example.com", "b@example.com"},
		Subject:  "Your sign-in link ✓",
		TextBody: "Visit http://localhost:5173/login?token=abc\n",
		HTMLBody: `<p><a href="http://localhost:5173/login?token=abc">Continue</a></p>`,
	}, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("To"); got != "a@example.com, b@example.com" {
		t.Fatalf("To = %q", got)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Your sign-in link ✓" {
		t.Fatalf("Subject = %q, %v", subject, err)
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@foc.local>") {
		t.Fatalf("Message-ID = %q", msg.Header.Get("Message-ID"))
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, %v", mediaType, err)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	var types, bodies []string
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, part.Header.Get("Content-Type"))
		bodies = append(bodies, string(body))
	}
	if len(types) != 2 || !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Fatalf("part types = %v", types)
	}
	if !strings.Contains(bodies[0], "token=abc") || !strings.Contains(bodies[1], `href="http://localhost:5173/login?token=abc"`) {
		t.Fatalf("part bodies = %q", bodies)
	}
}

func TestBuildMessageSinglePart(t *testing.T) {
	raw, err := buildMessage(&mail.Address{Address: "no-reply@foc.local"},
		Email{To: []string{"a@example.com"}, Subject: "Hi", TextBody: "plain"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("Content-Type"); got != "text/plain; charset=UTF-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	sender := SMTPSender{Addr: "127.0.0.1:1", From: "no-reply@foc.local"}
	for _, e := range []Email{
		{To: []string{"a@example.com\r\nBcc: evil@example.com"}, Subject: "Hi", TextBody: "x"},
		{To: []string{"a@example.com"}, Subject: "Hi\nBcc: evil@example.com", TextBody: "x"},
	} {
		if err := sender.Send(context.Background(), e); !errors.Is(err, errHeaderInjection) {
			t.Fatalf("Send(%q) error = %v", e.To, err)
		}
	}
	if err := sender.Send(context.Background(), Email{Subject: "Hi"}); err == nil {
		t.Fatal("Send without recipients succeeded")
	}
}

func TestSendDeliversThroughSMTP(t *testing.T) {
	server := startFakeSMTP(t, true)
	sender := SMTPSender{Addr: server.addr, From: "FoC <no-reply@foc.local>"}
	err := sender.Send(context.Background(), Email{
		To: []string{"user@example.com"}, Subject: "Link", TextBody: "hello", HTMLBody: "<p>hello</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := server.wait(t)
	if got.from != "no-reply@foc.local" || len(got.rcpts) != 1 || got.rcpts[0] != "user@example.com" {
		t.Fatalf("envelope = %+v", got)
	}
	if !strings.Contains(got.data, "Subject: Link") || !strings.Contains(got.data, "multipart/alternative") {
		t.Fatalf("data = %q", got.data)
	}
}

func TestSendRespectsContextDeadline(t *testing.T) {
	server := startFakeSMTP(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := SMTPSender{Addr: server.addr, From: "no-reply@foc.local"}.
		Send(ctx, Email{To: []string{"user@example.com"}, Subject: "Link", TextBody: "hello"})
	if err == nil {
		t.Fatal("Send to a silent server succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Send took %v; deadline ignored", elapsed)
	}
}

type delivery struct {
	from  string
	rcpts []string
	data  string
}

type fakeSMTP struct {
	addr      string
	delivered chan delivery
}

func (f *fakeSMTP) wait(t *testing.T) delivery {
	t.Helper()
	select {
	case d := <-f.delivered:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("no message delivered")
		return delivery{}
	}
}

// startFakeSMTP accepts connections and, when greet is set, speaks the minimum
// SMTP needed by net/smtp: no STARTTLS and no AUTH, like Mailpit's defaults.
// With greet unset it accepts and stays silent.
func startFakeSMTP(t *testing.T, greet bool) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTP{addr: listener.Addr().String(), delivered: make(chan delivery, 1)}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				if !greet {
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				serveSMTP(conn, server.delivered)
			}()
		}
	}()
	return server
}

func serveSMTP(conn net.Conn, delivered chan<- delivery) {
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 fake ESMTP")
	var d delivery
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			reply("250-fake")
			reply("250 8BITMIME")
		case strings.HasPrefix(command, "MAIL FROM:"):
			d.from = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			if i := strings.IndexByte(d.from, '>'); i >= 0 {
				d.from = d.from[:i]
			}
			reply("250 OK")
		case strings.HasPrefix(command, "RCPT TO:"):
			d.rcpts = append(d.rcpts, strings.Trim(line[len("RCPT TO:"):], "<> "))
			reply("250 OK")
		case command == "DATA":
			reply("354 go ahead")
			var data strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				data.WriteString(dataLine)
			}
			d.data = data.String()
			reply("250 queued")
			delivered <- d
		case command == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 OK")
		}
	}
}
