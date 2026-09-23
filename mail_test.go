package main

import (
	"bytes"
	"io"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestComposeMessage(t *testing.T) {
	link := "https://hello.example.com/verify?token=abc_-123"
	date := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	raw := composeMessage("Jellyfin Access <you@example.com>", "alice@example.com", link, date)
	if !bytes.Contains(raw, []byte("\r\n\r\n")) || bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("message does not use CRLF line endings")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"From":         "Jellyfin Access <you@example.com>",
		"To":           "alice@example.com",
		"Subject":      "Your Jellyfin access link",
		"Date":         date.Format(time.RFC1123Z),
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=utf-8",
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
