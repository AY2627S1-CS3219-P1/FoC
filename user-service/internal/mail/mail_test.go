package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeSMTP accepts one message and returns the DATA section.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan string, 1)
	go func() {
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r, w := bufio.NewReader(c), bufio.NewWriter(c)
		say := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
		say("220 fake")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					say("250 ok")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.Fields(line)[0]); cmd {
			case "EHLO", "HELO":
				say("250 fake")
			case "DATA":
				inData = true
				say("354 go")
			case "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPSend(t *testing.T) {
	addr, got := fakeSMTP(t)
	err := SMTP{Addr: addr, From: "no-reply@x.com"}.Send(context.Background(), Message{
		To: "a@u.nus.edu", Subject: "Hi", Body: "line1\nline2",
	})
	if err != nil {
		t.Fatal(err)
	}
	data := <-got
	for _, want := range []string{"To: a@u.nus.edu", "Subject: Hi", "line1\r\nline2"} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %q in %q", want, data)
		}
	}
}

func TestSMTPRejectsHeaderInjection(t *testing.T) {
	err := SMTP{Addr: "127.0.0.1:1", From: "x@x.com"}.Send(context.Background(), Message{To: "a@x.com\r\nBcc: evil@x.com"})
	if err == nil || !strings.Contains(err.Error(), "injection") {
		t.Fatalf("got %v", err)
	}
}
