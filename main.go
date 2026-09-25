// takeyourcoat is a minimal, self-hosted replacement for Knocknoc-style
// just-in-time network access. A person proves control of a trusted email
// address, and the public IPv4 address they are on is allowlisted for a
// limited time. The reverse proxy asks this service on every request
// whether the client is unlocked, so whatever it fronts stays hidden from
// everyone else.
package main

import (
	"context"
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed static
var staticFS embed.FS

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /request", s.handleRequest)
	mux.HandleFunc("GET /verify", s.handleVerifyGet)
	mux.HandleFunc("POST /verify", s.handleVerifyPost)
	mux.HandleFunc("GET /check", s.handleCheck)
	files := http.FileServerFS(staticFS)
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
	return s.securityHeaders(mux)
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	allow, err := newAllowlist(cfg.StateFile, time.Duration(cfg.WhitelistTTL))
	if err != nil {
		log.Fatalf("allowlist: %v", err)
	}
	s := newServer(cfg, allow, func(to, link string) error { return sendMagicLink(cfg.SMTP, to, link) })
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
