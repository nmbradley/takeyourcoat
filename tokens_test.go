package main

import (
	"encoding/base64"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestTokenIssuePeekConsume(t *testing.T) {
	s := newTokenStore(15 * time.Minute)
	tok, err := s.issue("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := base64.RawURLEncoding.DecodeString(tok); err != nil || len(b) != 32 {
		t.Fatalf("token %q is not 32 bytes of base64url: %v", tok, err)
	}
	for i := 0; i < 2; i++ {
		if email, ok := s.peek(tok); !ok || email != "alice@example.com" {
			t.Fatalf("peek %d: got %q, %v", i, email, ok)
		}
	}
	if email, ok := s.consume(tok); !ok || email != "alice@example.com" {
		t.Fatalf("consume: got %q, %v", email, ok)
	}
	if _, ok := s.consume(tok); ok {
		t.Error("token consumed twice")
	}
	if _, ok := s.peek(tok); ok {
		t.Error("peek succeeded after consume")
	}
	if _, ok := s.peek("nonsense"); ok {
		t.Error("unknown token accepted")
	}
}

func TestTokenStoresOnlyHash(t *testing.T) {
	s := newTokenStore(time.Minute)
	tok, _ := s.issue("alice@example.com")
	for k := range s.m {
		if string(k[:]) == tok {
			t.Error("raw token stored as key")
		}
	}
}

func TestTokenExpiry(t *testing.T) {
	clock := &fakeClock{time.Unix(1_000_000, 0)}
	s := newTokenStore(15 * time.Minute)
	s.now = clock.now
	tok, _ := s.issue("alice@example.com")
	other, _ := s.issue("bob@example.com")
	clock.t = clock.t.Add(15*time.Minute + time.Second)
	if _, ok := s.peek(tok); ok {
		t.Error("expired token accepted by peek")
	}
	if _, ok := s.consume(other); ok {
		t.Error("expired token accepted by consume")
	}
	if len(s.m) != 0 {
		t.Errorf("expired entries not purged: %d left", len(s.m))
	}
}

func TestLimiterSlidingWindow(t *testing.T) {
	clock := &fakeClock{time.Unix(1_000_000, 0)}
	l := newLimiter(3, time.Hour)
	l.now = clock.now
	for i := 0; i < 3; i++ {
		if !l.allow("alice@example.com") {
			t.Fatalf("request %d denied", i)
		}
		clock.t = clock.t.Add(10 * time.Minute)
	}
	if l.allow("alice@example.com") {
		t.Error("4th request within the hour allowed")
	}
	if !l.allow("bob@example.com") {
		t.Error("limit leaked across emails")
	}
	clock.t = clock.t.Add(30*time.Minute + time.Second) // first request now outside the window
	if !l.allow("alice@example.com") {
		t.Error("request denied after oldest left the window")
	}
	if l.allow("alice@example.com") {
		t.Error("window did not keep the other two requests")
	}
}
