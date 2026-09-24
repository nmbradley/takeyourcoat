package main

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

//go:embed templates
var templateFS embed.FS

// pages maps a page name to its parsed template set: the shared layout plus
// that page's "body" definition. Executing the set renders the layout.
var pages = func() map[string]*template.Template {
	m := map[string]*template.Template{}
	for _, name := range []string{"index", "sent", "confirm", "success", "error", "locked"} {
		m[name] = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return m
}()

const badIPMessage = "This portal only works with public IPv4 addresses. Your connection arrived over IPv6 or from a private network, which is not supported. Try again with IPv6 turned off, or from your home Wi-Fi."

type server struct {
	cfg     *Config
	allow   *allowlist
	tokens  *tokenStore
	limiter *limiter // per email and client IP
	capper  *limiter // per email across all IPs, a spam cap
	trusted map[string]bool
	send    func(to, link string) error
}

func newServer(cfg *Config, allow *allowlist, send func(to, link string) error) *server {
	trusted := map[string]bool{}
	for _, e := range cfg.TrustedEmails {
		trusted[e] = true
	}
	return &server{
		cfg:     cfg,
		allow:   allow,
		tokens:  newTokenStore(time.Duration(cfg.TokenTTL)),
		limiter: newLimiter(cfg.RequestsPerEmailPerHour, time.Hour),
		capper:  newLimiter(4*cfg.RequestsPerEmailPerHour, time.Hour),
		trusted: trusted,
		send:    send,
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000")
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
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 && slices.ContainsFunc(s.cfg.proxies, func(p netip.Prefix) bool { return p.Contains(ip) }) {
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
	ip, ok := s.clientIP(r)
	if ok && s.trusted[email] && s.limiter.allow(email+"|"+ip.String()) && s.capper.allow(email) {
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
	// Consume only after the add is saved, so a failed save leaves the link usable.
	tok := r.PostForm.Get("token")
	email, ok := s.tokens.peek(tok)
	if !ok {
		render(w, http.StatusBadRequest, "error", "That link is invalid or has expired.")
		return
	}
	old, err := s.allow.add(ip, email)
	if err != nil {
		log.Printf("allowlist add %s: %v", ip, err)
		render(w, http.StatusInternalServerError, "error", "Something went wrong unlocking your network. Please try again later.")
		return
	}
	s.tokens.consume(tok)
	if old.IsValid() {
		log.Printf("unlocked %s for %s, replacing %s", ip, email, old)
	} else {
		log.Printf("unlocked %s for %s", ip, email)
	}
	render(w, http.StatusOK, "success", struct {
		IP    netip.Addr
		Hours int
	}{ip, int(time.Duration(s.cfg.WhitelistTTL).Hours())})
}

// handleCheck is Caddy's forward_auth target: 200 if the caller's IP is
// unlocked, otherwise 403 with the locked page, which Caddy passes through.
func (s *server) handleCheck(w http.ResponseWriter, r *http.Request) {
	msg := "This network is not unlocked. Open the portal from a device on this network to unlock it."
	ip, ok := s.clientIP(r)
	if !ok {
		msg = badIPMessage
	} else if s.allow.allowed(ip) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
		return
	}
	render(w, http.StatusForbidden, "locked", struct{ Message, PublicURL string }{msg, s.cfg.PublicURL})
}
