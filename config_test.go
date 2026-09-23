package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var envKeys = []string{
	"TYC_CONFIG", "TYC_LISTEN", "TYC_PUBLIC_URL", "TYC_TRUSTED_PROXIES", "TYC_TRUSTED_EMAILS",
	"TYC_IPSET_NAME", "TYC_WHITELIST_TTL", "TYC_TOKEN_TTL", "TYC_REQUESTS_PER_EMAIL_PER_HOUR",
	"TYC_SMTP_HOST", "TYC_SMTP_PORT", "TYC_SMTP_USERNAME", "TYC_SMTP_PASSWORD", "TYC_SMTP_FROM",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	clearEnv(t)
	t.Setenv("TYC_PUBLIC_URL", "https://hello.example.com")
	t.Setenv("TYC_TRUSTED_EMAILS", " Alice@Example.com , bob@example.com")
	t.Setenv("TYC_SMTP_HOST", "smtp.example.com")
	t.Setenv("TYC_SMTP_USERNAME", "user")
	t.Setenv("TYC_SMTP_PASSWORD", "secret")
	t.Setenv("TYC_SMTP_FROM", "Jellyfin Access <you@example.com>")
}

func writeConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYC_CONFIG", path)
}

const fileConfig = `{
  "public_url": "https://file.example.com",
  "trusted_emails": ["Carol@Example.com"],
  "token_ttl": "5m",
  "smtp": {"host": "smtp.file.com", "username": "u", "password": "p", "from": "f@file.com", "port": 2525}
}`

func TestLoadConfigEnvOnly(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.IpsetName != "jellyfin_clients" || cfg.SMTP.Port != 587 ||
		cfg.RequestsPerEmailPerHour != 3 || time.Duration(cfg.WhitelistTTL) != 72*time.Hour ||
		time.Duration(cfg.TokenTTL) != 15*time.Minute {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if strings.Join(cfg.TrustedEmails, ",") != "alice@example.com,bob@example.com" {
		t.Errorf("emails not normalised: %q", cfg.TrustedEmails)
	}
	if len(cfg.proxies) != 2 {
		t.Errorf("default proxies: %v", cfg.proxies)
	}
}

func TestLoadConfigFileOnly(t *testing.T) {
	clearEnv(t)
	writeConfig(t, fileConfig)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://file.example.com" || cfg.SMTP.Port != 2525 ||
		time.Duration(cfg.TokenTTL) != 5*time.Minute || cfg.TrustedEmails[0] != "carol@example.com" {
		t.Errorf("file values not loaded: %+v", cfg)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	clearEnv(t)
	writeConfig(t, fileConfig)
	t.Setenv("TYC_PUBLIC_URL", "https://env.example.com")
	t.Setenv("TYC_TOKEN_TTL", "1m")
	t.Setenv("TYC_SMTP_PORT", "25")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://env.example.com" || time.Duration(cfg.TokenTTL) != time.Minute ||
		cfg.SMTP.Port != 25 || cfg.SMTP.Host != "smtp.file.com" {
		t.Errorf("env did not override file: %+v", cfg)
	}
}

func TestLoadConfigMissingRequiredNamesField(t *testing.T) {
	for _, key := range []string{"TYC_PUBLIC_URL", "TYC_TRUSTED_EMAILS", "TYC_SMTP_HOST", "TYC_SMTP_USERNAME", "TYC_SMTP_PASSWORD", "TYC_SMTP_FROM"} {
		t.Run(key, func(t *testing.T) {
			setRequiredEnv(t)
			os.Unsetenv(key)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("want error naming %s, got %v", key, err)
			}
		})
	}
}

func TestLoadConfigMalformedDuration(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TYC_WHITELIST_TTL", "three days")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TYC_WHITELIST_TTL") {
		t.Errorf("want error naming TYC_WHITELIST_TTL, got %v", err)
	}

	clearEnv(t)
	writeConfig(t, strings.Replace(fileConfig, `"5m"`, `"soon"`, 1))
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "soon") {
		t.Errorf("want error for malformed file duration, got %v", err)
	}
}

func TestLoadConfigMalformedValues(t *testing.T) {
	for key, val := range map[string]string{
		"TYC_SMTP_PORT":                   "abc",
		"TYC_REQUESTS_PER_EMAIL_PER_HOUR": "0",
		"TYC_TRUSTED_PROXIES":             "not-an-ip",
		"TYC_PUBLIC_URL":                  "hello.example.com",
		"TYC_SMTP_FROM":                   "not an address",
	} {
		t.Run(key, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(key, val)
			if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("want error naming %s, got %v", key, err)
			}
		})
	}
}
