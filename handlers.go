package main

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

const layoutHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>takeyourcoat</title>
<link rel="stylesheet" href="/static/pico.classless.min.css">
</head>
<body>
<main>
<h1>takeyourcoat</h1>
<article>
{{template "body" .}}
</article>
</main>
</body>
</html>
`

var pages = func() map[string]*template.Template {
	bodies := map[string]string{
		"index": `<p>Enter your email address to get a link that unlocks Jellyfin for this network.</p>
<form method="post" action="/request">
<label>Email <input type="email" name="email" autocomplete="email" required></label>
<button type="submit">Send link</button>
</form>`,
		"sent": `<p>If that address is on the list, a link is on its way. Check your inbox.</p>
<p>Open the link on a device connected to the network you want to unlock.</p>`,
		"confirm": `<p>Your network's address is <strong>{{.IP}}</strong>.</p>
<p>Confirm to unlock Jellyfin for every device on this network.</p>
<form method="post" action="/verify">
<input type="hidden" name="token" value="{{.Token}}">
<button type="submit">Unlock</button>
</form>`,
		"success": `<p>Done. <strong>{{.IP}}</strong> can reach Jellyfin for the next {{.Hours}} hours.</p>`,
		"error":   `<p>{{.}}</p><p><a href="/">Start again</a></p>`,
	}
	m := map[string]*template.Template{}
	for name, body := range bodies {
		t := template.Must(template.New("layout").Parse(layoutHTML))
		m[name] = template.Must(t.New("body").Parse(body))
	}
	return m
}()

const badIPMessage = "This portal only works with public IPv4 addresses. Your connection arrived over IPv6 or from a private network, which is not supported. Try again with IPv6 turned off, or from your home Wi-Fi."

type server struct {
	cfg     *Config
	tokens  *tokenStore
	limiter *limiter
	trusted map[string]bool
	send    func(to, link string) error
}

func newServer(cfg *Config, send func(to, link string) error) *server {
	trusted := map[string]bool{}
	for _, e := range cfg.TrustedEmails {
		trusted[e] = true
	}
	return &server{
		cfg:     cfg,
		tokens:  newTokenStore(time.Duration(cfg.TokenTTL)),
		limiter: newLimiter(cfg.RequestsPerEmailPerHour, time.Hour),
		trusted: trusted,
		send:    send,
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func render(w http.ResponseWriter, status int, page string, data any) {
	var buf bytes.Buffer
	if err := pages[page].Execute(&buf, data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// clientIP returns the caller's address, taking the last X-Forwarded-For hop
// only when the direct peer is a trusted proxy. ok is false unless it is a
// public IPv4 address.
func (s *server) clientIP(r *http.Request) (ip netip.Addr, ok bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	ip = ap.Addr().Unmap()
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 && slices.Contains(s.cfg.proxies, ip) {
		hops := strings.Split(xff[len(xff)-1], ",")
		if ip, err = netip.ParseAddr(strings.TrimSpace(hops[len(hops)-1])); err != nil {
			return netip.Addr{}, false
		}
		ip = ip.Unmap()
	}
	return ip, publicIPv4(ip)
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, "index", nil)
}

func (s *server) handleRequest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		render(w, http.StatusBadRequest, "error", "That request could not be read.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email")))
	if s.trusted[email] && s.limiter.allow(email) {
		if tok, err := s.tokens.issue(email); err != nil {
			log.Printf("issue token: %v", err)
		} else {
			link := strings.TrimRight(s.cfg.PublicURL, "/") + "/verify?token=" + tok
			go func() {
				if err := s.send(email, link); err != nil {
					log.Printf("send mail to %s: %v", email, err)
				}
			}()
		}
	}
	render(w, http.StatusOK, "sent", nil)
}

func (s *server) handleVerifyGet(w http.ResponseWriter, r *http.Request) {
	ip, ok := s.clientIP(r)
	if !ok {
		render(w, http.StatusBadRequest, "error", badIPMessage)
		return
	}
	tok := r.URL.Query().Get("token")
	if _, ok := s.tokens.peek(tok); !ok {
		render(w, http.StatusBadRequest, "error", "That link is invalid or has expired.")
		return
	}
	render(w, http.StatusOK, "confirm", struct {
		IP    netip.Addr
		Token string
	}{ip, tok})
}

func (s *server) handleVerifyPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		render(w, http.StatusBadRequest, "error", "That request could not be read.")
		return
	}
	ip, ok := s.clientIP(r)
	if !ok {
		render(w, http.StatusBadRequest, "error", badIPMessage)
		return
	}
	email, ok := s.tokens.consume(r.PostForm.Get("token"))
	if !ok {
		render(w, http.StatusBadRequest, "error", "That link is invalid or has expired.")
		return
	}
	ttl := time.Duration(s.cfg.WhitelistTTL)
	if err := ipsetAdd(s.cfg.IpsetName, ip, ttl); err != nil {
		log.Printf("ipset add %s: %v", ip, err)
		render(w, http.StatusInternalServerError, "error", "Something went wrong unlocking your network. Please try again later.")
		return
	}
	log.Printf("whitelisted %s for %s", ip, email)
	render(w, http.StatusOK, "success", struct {
		IP    netip.Addr
		Hours int
	}{ip, int(ttl.Hours())})
}
