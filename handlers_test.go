package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sentMail struct{ to, link string }

func newTestServer(t *testing.T, proxies ...string) (*server, http.Handler, chan sentMail) {
	t.Helper()
	if proxies == nil {
		proxies = []string{"127.0.0.1/32", "::1/128"}
	}
	cfg := &Config{
		PublicURL:               "https://hello.example.com/",
		TrustedEmails:           []string{"alice@example.com"},
		StateFile:               filepath.Join(t.TempDir(), "allowlist.json"),
		WhitelistTTL:            Duration(72 * time.Hour),
		TokenTTL:                Duration(15 * time.Minute),
		RequestsPerEmailPerHour: 3,
	}
	for _, p := range proxies {
		cfg.proxies = append(cfg.proxies, netip.MustParsePrefix(p))
	}
	allow, err := newAllowlist(cfg.StateFile, time.Duration(cfg.WhitelistTTL))
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan sentMail, 10)
	s := newServer(cfg, allow, func(to, link string) error {
		sent <- sentMail{to, link}
		return nil
	})
	return s, s.routes(), sent
}

func do(h http.Handler, method, target, remote string, form url.Values, xff ...string) *httptest.ResponseRecorder {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if remote != "" {
		r.RemoteAddr = remote
	}
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIndexRendersFormWithSecurityHeaders(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := do(h, "GET", "/", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="email"`) || !strings.Contains(w.Body.String(), `action="/request"`) {
		t.Fatalf("index: %d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(w.Body.String(), "<!doctype html>") || !strings.Contains(w.Body.String(), `href="/static/pico.classless.min.css"`) {
		t.Fatalf("index not wrapped in layout: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), "<h1>May I Take Your Coat?</h1>") {
		t.Fatalf("index missing header: %s", w.Body)
	}
	for k, v := range map[string]string{
		"Content-Security-Policy":   "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"Strict-Transport-Security": "max-age=31536000",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Cache-Control":             "no-store",
		"Content-Type":              "text/html; charset=utf-8",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if do(h, "GET", "/nope", "", nil).Code != 404 {
		t.Error("unknown path not 404")
	}
	if do(h, "POST", "/", "", url.Values{}).Code != 405 {
		t.Error("POST / not 405")
	}
}

func TestRequestSameResponseForKnownAndUnknown(t *testing.T) {
	s, h, sent := newTestServer(t)
	known := do(h, "POST", "/request", "", url.Values{"email": {"  Alice@Example.COM "}})
	unknown := do(h, "POST", "/request", "", url.Values{"email": {"mallory@example.com"}})
	if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Fatalf("responses differ:\n%d %s\n%d %s", known.Code, known.Body, unknown.Code, unknown.Body)
	}
	select {
	case m := <-sent:
		if m.to != "alice@example.com" || !strings.HasPrefix(m.link, "https://hello.example.com/verify?token=") {
			t.Errorf("unexpected mail %+v", m)
		}
		if _, ok := s.tokens.peek(strings.TrimPrefix(m.link, "https://hello.example.com/verify?token=")); !ok {
			t.Error("mailed token not in store")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no mail sent for known email")
	}
	if len(s.tokens.m) != 1 {
		t.Errorf("unknown email minted a token: %d tokens", len(s.tokens.m))
	}
}

func TestRequestRateLimitedLooksIdentical(t *testing.T) {
	_, h, sent := newTestServer(t)
	var first string
	for i := 0; i < 5; i++ {
		w := do(h, "POST", "/request", "", url.Values{"email": {"alice@example.com"}})
		if i == 0 {
			first = w.Body.String()
		} else if w.Body.String() != first || w.Code != 200 {
			t.Fatalf("request %d differs", i)
		}
	}
	for i := 0; i < 3; i++ {
		<-sent
	}
	select {
	case <-sent:
		t.Error("more than 3 mails sent within the hour")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := do(h, "POST", "/request", "", url.Values{"email": {strings.Repeat("a", 5000)}})
	if w.Code != 400 {
		t.Errorf("oversized body: %d", w.Code)
	}
}

func TestVerifyXFFIgnoredFromUntrustedRemote(t *testing.T) {
	s, h, _ := newTestServer(t)
	tok, _ := s.tokens.issue("alice@example.com")
	w := do(h, "GET", "/verify?token="+tok, "203.0.113.5:4444", nil, "198.51.100.7")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "203.0.113.5") || strings.Contains(w.Body.String(), "198.51.100.7") {
		t.Errorf("XFF not ignored: %d %s", w.Code, w.Body)
	}
}

func TestVerifyXFFLastHopHonouredFromTrustedProxy(t *testing.T) {
	s, h, _ := newTestServer(t)
	tok, _ := s.tokens.issue("alice@example.com")
	w := do(h, "GET", "/verify?token="+tok, "127.0.0.1:4444", nil, "8.8.8.8", "1.2.3.4, 198.51.100.7")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "198.51.100.7") || strings.Contains(body, "1.2.3.4") || strings.Contains(body, "8.8.8.8") {
		t.Errorf("last XFF hop not used: %d %s", w.Code, body)
	}
}

func TestVerifyRejectsIPv6AndPrivate(t *testing.T) {
	s, h, _ := newTestServer(t)
	tok, _ := s.tokens.issue("alice@example.com")
	for _, tc := range []struct{ remote, xff string }{
		{"[2001:db8::1]:4444", ""},
		{"10.0.0.5:4444", ""},
		{"127.0.0.1:4444", "192.168.1.20"},
		{"127.0.0.1:4444", "2001:db8::1"},
		{"127.0.0.1:4444", "garbage"},
	} {
		var xff []string
		if tc.xff != "" {
			xff = []string{tc.xff}
		}
		g := do(h, "GET", "/verify?token="+tok, tc.remote, nil, xff...)
		p := do(h, "POST", "/verify", tc.remote, url.Values{"token": {tok}}, xff...)
		for _, w := range []*httptest.ResponseRecorder{g, p} {
			if w.Code != 400 || !strings.Contains(w.Body.String(), "IPv6") {
				t.Errorf("%+v: %d %s", tc, w.Code, w.Body)
			}
		}
	}
	if len(s.allow.m) != 0 {
		t.Errorf("allowlist changed: %v", s.allow.m)
	}
	if _, ok := s.tokens.peek(tok); !ok {
		t.Error("rejected IP consumed the token")
	}
}

func TestVerifyGetDoesNotConsume(t *testing.T) {
	s, h, _ := newTestServer(t)
	tok, _ := s.tokens.issue("alice@example.com")
	for i := 0; i < 2; i++ {
		w := do(h, "GET", "/verify?token="+tok, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="token" value="`+tok+`"`) || !strings.Contains(w.Body.String(), `method="post"`) ||
			!strings.Contains(w.Body.String(), "Only confirm from your home Wi-Fi") {
			t.Fatalf("GET %d: %d %s", i, w.Code, w.Body)
		}
	}
	if _, ok := s.tokens.peek(tok); !ok {
		t.Error("GET consumed the token")
	}
	if w := do(h, "GET", "/verify?token=bogus", "", nil); w.Code != 400 {
		t.Errorf("bogus token: %d", w.Code)
	}
}

func TestVerifyPostAddsOnceThenRejectsReuse(t *testing.T) {
	s, h, _ := newTestServer(t)
	tok, _ := s.tokens.issue("alice@example.com")
	w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "203.0.113.5") {
		t.Fatalf("first POST: %d %s", w.Code, w.Body)
	}
	if s.allow.m[netip.MustParseAddr("203.0.113.5")].Email != "alice@example.com" {
		t.Errorf("entry not recorded for alice: %v", s.allow.m)
	}
	if w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}}); w.Code != 400 {
		t.Errorf("reused token: %d", w.Code)
	}
	b, err := os.ReadFile(s.cfg.StateFile)
	if err != nil || !strings.Contains(string(b), `"203.0.113.5":`) {
		t.Errorf("state file = %s, %v", b, err)
	}
}

func TestStaticCSS(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, p := range []string{"/static/pico.classless.min.css", "/static/site.css"} {
		if w := do(h, "GET", p, "", nil); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
			t.Fatalf("%s: %d %q", p, w.Code, w.Header().Get("Content-Type"))
		}
	}
	w := do(h, "GET", "/static/pico.classless.min.css", "", nil)
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing on static")
	}
}

func unlock(t *testing.T, s *server, h http.Handler, remote string, xff ...string) {
	t.Helper()
	tok, _ := s.tokens.issue("alice@example.com")
	if w := do(h, "POST", "/verify", remote, url.Values{"token": {tok}}, xff...); w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
}

func assertLocked(t *testing.T, w *httptest.ResponseRecorder, msg string) {
	t.Helper()
	body := w.Body.String()
	if w.Code != 403 || !strings.Contains(body, msg) || !strings.Contains(body, `href="https://hello.example.com/"`) ||
		!strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("want locked page: %d %s", w.Code, body)
	}
}

func TestCheckLockedUntilVerified(t *testing.T) {
	s, h, _ := newTestServer(t)
	assertLocked(t, do(h, "GET", "/check", "203.0.113.5:4444", nil), "This network is not unlocked.")
	unlock(t, s, h, "203.0.113.5:4444")
	w := do(h, "GET", "/check", "203.0.113.5:5555", nil)
	if w.Code != 200 || w.Body.String() != "ok" || w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("unlocked check: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Type"))
	}
	assertLocked(t, do(h, "GET", "/check", "198.51.100.7:4444", nil), "This network is not unlocked.")
}

func TestCheckViaTrustedProxy(t *testing.T) {
	s, h, _ := newTestServer(t)
	unlock(t, s, h, "127.0.0.1:4444", "203.0.113.5")
	if w := do(h, "GET", "/check", "127.0.0.1:4444", nil, "203.0.113.5"); w.Code != 200 {
		t.Errorf("forwarded unlocked IP: %d %s", w.Code, w.Body)
	}
	for _, xff := range []string{"2001:db8::1", "192.168.1.20", "garbage"} {
		assertLocked(t, do(h, "GET", "/check", "127.0.0.1:4444", nil, xff), "IPv6")
	}
}

func TestCheckXFFIgnoredFromUntrustedRemote(t *testing.T) {
	s, h, _ := newTestServer(t)
	unlock(t, s, h, "203.0.113.5:4444")
	assertLocked(t, do(h, "GET", "/check", "198.51.100.7:4444", nil, "203.0.113.5"), "This network is not unlocked.")
}

func TestCheckCIDRTrustedProxy(t *testing.T) {
	s, h, _ := newTestServer(t, "172.28.0.0/24")
	unlock(t, s, h, "172.28.0.3:4444", "203.0.113.5")
	if w := do(h, "GET", "/check", "172.28.0.7:4444", nil, "203.0.113.5"); w.Code != 200 {
		t.Errorf("CIDR proxy not trusted: %d %s", w.Code, w.Body)
	}
	assertLocked(t, do(h, "GET", "/check", "172.29.0.7:4444", nil, "203.0.113.5"), "IPv6")
}

func mails(sent chan sentMail) int {
	n := 0
	for {
		select {
		case <-sent:
			n++
		case <-time.After(100 * time.Millisecond):
			return n
		}
	}
}

func TestRequestLimitPerEmailAndIP(t *testing.T) {
	_, h, sent := newTestServer(t)
	for i := 0; i < 4; i++ {
		do(h, "POST", "/request", "198.51.100.7:4444", url.Values{"email": {"alice@example.com"}})
	}
	do(h, "POST", "/request", "203.0.113.5:4444", url.Values{"email": {"alice@example.com"}})
	if n := mails(sent); n != 4 {
		t.Errorf("mails = %d, want 3 from the first IP and 1 from the second", n)
	}
}

func TestRequestPerEmailCapAcrossIPs(t *testing.T) {
	_, h, sent := newTestServer(t)
	for i := 0; i < 5; i++ {
		for j := 0; j < 3; j++ {
			do(h, "POST", "/request", fmt.Sprintf("198.51.100.%d:4444", i+1), url.Values{"email": {"alice@example.com"}})
		}
	}
	if n := mails(sent); n != 12 {
		t.Errorf("mails = %d, want cap of 12", n)
	}
}

func TestRequestFromBadIPDoesNoWork(t *testing.T) {
	s, h, sent := newTestServer(t)
	ok := do(h, "POST", "/request", "203.0.113.5:4444", url.Values{"email": {"mallory@example.com"}})
	for _, remote := range []string{"[2001:db8::1]:4444", "10.0.0.5:4444"} {
		w := do(h, "POST", "/request", remote, url.Values{"email": {"alice@example.com"}})
		if w.Code != 200 || w.Body.String() != ok.Body.String() {
			t.Errorf("%s: %d %s", remote, w.Code, w.Body)
		}
	}
	if n := mails(sent); n != 0 || len(s.tokens.m) != 0 {
		t.Errorf("bad IP did work: %d mails, %d tokens", n, len(s.tokens.m))
	}
}

func TestVerifyPostSaveFailureKeepsToken(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s, h, _ := newTestServer(t)
	dir := filepath.Dir(s.cfg.StateFile)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	tok, _ := s.tokens.issue("alice@example.com")
	if w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}}); w.Code != 500 {
		t.Fatalf("save failure: %d %s", w.Code, w.Body)
	}
	if _, ok := s.tokens.peek(tok); !ok {
		t.Fatal("failed save burned the token")
	}
	os.Chmod(dir, 0o700)
	if w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}}); w.Code != 200 {
		t.Errorf("retry: %d %s", w.Code, w.Body)
	}
}
