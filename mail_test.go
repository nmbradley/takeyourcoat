package main

import (
	"bytes"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestComposeMessage(t *testing.T) {
	link := "https://hello.example.com/verify?token=abc_-123"
	date := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	id := "<0123456789abcdef0123456789abcdef@example.com>"
	raw := composeMessage("Access Portal <you@example.com>", "alice@example.com", link, id, date)
	if !bytes.Contains(raw, []byte("\r\n\r\n")) || bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("message does not use CRLF line endings")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"From":         "Access Portal <you@example.com>",
		"To":           "alice@example.com",
		"Subject":      "Your access link",
		"Date":         date.Format(time.RFC1123Z),
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=utf-8",
		"Message-ID":   id,
	} {
		if got := msg.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	body, _ := io.ReadAll(msg.Body)
	if !strings.Contains(string(body), link) {
		t.Errorf("body missing link:\n%s", body)
	}
}

func TestMessageID(t *testing.T) {
	a, b := messageID("you@example.com"), messageID("you@example.com")
	if !regexp.MustCompile(`^<[0-9a-f]{32}@example\.com>$`).MatchString(a) || a == b {
		t.Errorf("message ids %q %q", a, b)
	}
}
