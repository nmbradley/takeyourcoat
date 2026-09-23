package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration that unmarshals from a Go duration string.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

type Config struct {
	Listen                  string     `json:"listen"`
	PublicURL               string     `json:"public_url"`
	TrustedProxies          []string   `json:"trusted_proxies"`
	TrustedEmails           []string   `json:"trusted_emails"`
	IpsetName               string     `json:"ipset_name"`
	WhitelistTTL            Duration   `json:"whitelist_ttl"`
	TokenTTL                Duration   `json:"token_ttl"`
	RequestsPerEmailPerHour int        `json:"requests_per_email_per_hour"`
	SMTP                    SMTPConfig `json:"smtp"`

	proxies []netip.Addr
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Listen:                  "127.0.0.1:8080",
		TrustedProxies:          []string{"127.0.0.1", "::1"},
		IpsetName:               "jellyfin_clients",
		WhitelistTTL:            Duration(72 * time.Hour),
		TokenTTL:                Duration(15 * time.Minute),
		RequestsPerEmailPerHour: 3,
		SMTP:                    SMTPConfig{Port: 587},
	}
	if path := os.Getenv("TYC_CONFIG"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, cfg.validate()
}

func applyEnv(c *Config) error {
	var errs []error
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	list := func(key string, dst *[]string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = strings.Split(v, ",")
		}
	}
	num := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
			}
			*dst = n
		}
	}
	dur := func(key string, dst *Duration) {
		if v, ok := os.LookupEnv(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
			}
			*dst = Duration(d)
		}
	}
	str("TYC_LISTEN", &c.Listen)
	str("TYC_PUBLIC_URL", &c.PublicURL)
	list("TYC_TRUSTED_PROXIES", &c.TrustedProxies)
	list("TYC_TRUSTED_EMAILS", &c.TrustedEmails)
	str("TYC_IPSET_NAME", &c.IpsetName)
	dur("TYC_WHITELIST_TTL", &c.WhitelistTTL)
	dur("TYC_TOKEN_TTL", &c.TokenTTL)
	num("TYC_REQUESTS_PER_EMAIL_PER_HOUR", &c.RequestsPerEmailPerHour)
	str("TYC_SMTP_HOST", &c.SMTP.Host)
	num("TYC_SMTP_PORT", &c.SMTP.Port)
	str("TYC_SMTP_USERNAME", &c.SMTP.Username)
	str("TYC_SMTP_PASSWORD", &c.SMTP.Password)
	str("TYC_SMTP_FROM", &c.SMTP.From)
	return errors.Join(errs...)
}

func (c *Config) validate() error {
	var errs []error
	bad := func(field, msg string) { errs = append(errs, fmt.Errorf("%s: %s", field, msg)) }
	var emails []string
	for _, e := range c.TrustedEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			emails = append(emails, e)
		}
	}
	c.TrustedEmails = emails
	c.proxies = nil
	for _, p := range c.TrustedProxies {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		ip, err := netip.ParseAddr(p)
		if err != nil {
			bad("TYC_TRUSTED_PROXIES (trusted_proxies)", "invalid address "+strconv.Quote(p))
		}
		c.proxies = append(c.proxies, ip.Unmap())
	}
	required := map[string]string{
		"TYC_PUBLIC_URL (public_url)":       c.PublicURL,
		"TYC_SMTP_HOST (smtp.host)":         c.SMTP.Host,
		"TYC_SMTP_USERNAME (smtp.username)": c.SMTP.Username,
		"TYC_SMTP_PASSWORD (smtp.password)": c.SMTP.Password,
		"TYC_SMTP_FROM (smtp.from)":         c.SMTP.From,
		"TYC_LISTEN (listen)":               c.Listen,
		"TYC_IPSET_NAME (ipset_name)":       c.IpsetName,
	}
	for field, v := range required {
		if v == "" {
			bad(field, "required")
		}
	}
	if len(c.TrustedEmails) == 0 {
		bad("TYC_TRUSTED_EMAILS (trusted_emails)", "required")
	}
	if u, err := url.Parse(c.PublicURL); c.PublicURL != "" && (err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "") {
		bad("TYC_PUBLIC_URL (public_url)", "must be an absolute http(s) URL")
	}
	if _, err := mail.ParseAddress(c.SMTP.From); c.SMTP.From != "" && err != nil {
		bad("TYC_SMTP_FROM (smtp.from)", "must be an email address")
	}
	if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
		bad("TYC_SMTP_PORT (smtp.port)", "must be 1-65535")
	}
	if c.RequestsPerEmailPerHour < 1 {
		bad("TYC_REQUESTS_PER_EMAIL_PER_HOUR (requests_per_email_per_hour)", "must be at least 1")
	}
	if time.Duration(c.WhitelistTTL) < time.Second {
		bad("TYC_WHITELIST_TTL (whitelist_ttl)", "must be at least 1s")
	}
	if time.Duration(c.TokenTTL) < time.Second {
		bad("TYC_TOKEN_TTL (token_ttl)", "must be at least 1s")
	}
	return errors.Join(errs...)
}
