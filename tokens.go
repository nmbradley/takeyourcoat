package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

type tokenEntry struct {
	email  string
	expiry time.Time
}

// tokenStore maps sha256(token) to the email it was issued for.
type tokenStore struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[[sha256.Size]byte]tokenEntry
}

func newTokenStore(ttl time.Duration) *tokenStore {
	return &tokenStore{ttl: ttl, now: time.Now, m: map[[sha256.Size]byte]tokenEntry{}}
}

func (s *tokenStore) issue(email string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	s.m[sha256.Sum256([]byte(tok))] = tokenEntry{email, s.now().Add(s.ttl)}
	return tok, nil
}

func (s *tokenStore) peek(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	e, ok := s.m[sha256.Sum256([]byte(tok))]
	return e.email, ok
}

func (s *tokenStore) consume(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	key := sha256.Sum256([]byte(tok))
	e, ok := s.m[key]
	delete(s.m, key)
	return e.email, ok
}

// purge drops expired entries. Caller holds s.mu.
func (s *tokenStore) purge() {
	now := s.now()
	for k, e := range s.m {
		if !now.Before(e.expiry) {
			delete(s.m, k)
		}
	}
}

// limiter allows at most limit events per key within a sliding window.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	seen   map[string][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, now: time.Now, seen: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	recent := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.limit {
		l.seen[key] = recent
		return false
	}
	l.seen[key] = append(recent, now)
	return true
}
