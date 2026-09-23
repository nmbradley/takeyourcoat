package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type sentMail struct{ to, link string }

func newTestServer(t *testing.T) (*server, http.Handler, chan sentMail) {
	t.Helper()
	cfg := &Config{
		PublicURL:               "https://hello.example.com/",
		TrustedEmails:           []string{"alice@example.com"},
		proxies:                 []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")},
		IpsetName:               "jellyfin_clients",
		WhitelistTTL:            Duration(72 * time.Hour),
		TokenTTL:                Duration(15 * time.Minute),
		RequestsPerEmailPerHour: 3,
	}
	sent := make(chan sentMail, 10)
	s := newServer(cfg, func(to, link string) error {
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
	for k, v := range map[string]string{
		"Content-Security-Policy": "default-src 'none'; style-src 'self'; form-action 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Type":            "text/html; charset=utf-8",
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
	calls := fakeIpset(t)
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
	if len(*calls) != 0 {
		t.Errorf("ipset called: %q", *calls)
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
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="token" value="`+tok+`"`) || !strings.Contains(w.Body.String(), `method="post"`) {
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
	calls := fakeIpset(t)
	tok, _ := s.tokens.issue("alice@example.com")
	w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "203.0.113.5") {
		t.Fatalf("first POST: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/verify", "203.0.113.5:4444", url.Values{"token": {tok}}); w.Code != 400 {
		t.Errorf("reused token: %d", w.Code)
	}
	want := "add jellyfin_clients 203.0.113.5 timeout 259200 -exist"
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != want {
		t.Errorf("ipset calls = %q, want one %q", *calls, want)
	}
}

func TestStaticCSS(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := do(h, "GET", "/static/pico.classless.min.css", "", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("static: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing on static")
	}
}
