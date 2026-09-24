package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var visitor = netip.MustParseAddr("203.0.113.9")

func newTestAllowlist(t *testing.T) (*allowlist, *fakeClock, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "allowlist.json")
	a, err := newAllowlist(path, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1_000_000, 0)}
	a.now = clock.now
	return a, clock, path
}

func TestAllowlistAddThenAllowed(t *testing.T) {
	a, _, _ := newTestAllowlist(t)
	if a.allowed(visitor) {
		t.Fatal("allowed before add")
	}
	if err := a.add(visitor); err != nil {
		t.Fatal(err)
	}
	if !a.allowed(visitor) {
		t.Error("not allowed after add")
	}
	if a.allowed(netip.MustParseAddr("198.51.100.7")) {
		t.Error("other address allowed")
	}
}

func TestAllowlistExpiry(t *testing.T) {
	a, clock, _ := newTestAllowlist(t)
	a.add(visitor)
	clock.t = clock.t.Add(72*time.Hour - time.Second)
	if !a.allowed(visitor) {
		t.Error("expired early")
	}
	clock.t = clock.t.Add(time.Second)
	if a.allowed(visitor) {
		t.Error("allowed at expiry")
	}
	if len(a.m) != 0 {
		t.Errorf("expired entry not purged: %v", a.m)
	}
}

func TestAllowlistRefreshExtendsExpiry(t *testing.T) {
	a, clock, _ := newTestAllowlist(t)
	a.add(visitor)
	clock.t = clock.t.Add(48 * time.Hour)
	a.add(visitor)
	clock.t = clock.t.Add(48 * time.Hour)
	if !a.allowed(visitor) {
		t.Error("refresh did not extend expiry")
	}
}

func TestAllowlistRefusesNonPublicIPv4(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	for _, s := range []string{
		"10.0.0.1", "172.16.5.5", "192.168.1.1", "127.0.0.1", "169.254.1.1",
		"224.0.0.1", "0.0.0.0", "::1", "2001:db8::1", "::ffff:203.0.113.9", "fe80::1",
	} {
		if err := a.add(netip.MustParseAddr(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if err := a.add(netip.Addr{}); err == nil {
		t.Error("zero Addr accepted")
	}
	if len(a.m) != 0 {
		t.Errorf("refused addresses stored: %v", a.m)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("state file written for refused addresses: %v", err)
	}
}

func TestAllowlistPersistsAcrossRestart(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	a.now = time.Now // newAllowlist purges against the real clock
	if err := a.add(visitor); err != nil {
		t.Fatal(err)
	}
	b, err := newAllowlist(path, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !b.allowed(visitor) {
		t.Error("entry lost across restart")
	}
}

func TestAllowlistDropsExpiredOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	body := `{"203.0.113.9":"` + past + `","198.51.100.7":"` + future + `"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := newAllowlist(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.m[visitor]; ok || len(a.m) != 1 || !a.allowed(netip.MustParseAddr("198.51.100.7")) {
		t.Errorf("load kept %v", a.m)
	}
}

func TestAllowlistFileModeAndAtomicWrite(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	if err := a.add(visitor); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != "allowlist.json" {
		t.Errorf("unexpected files left behind: %v", entries)
	}
}

func TestAllowlistMissingFileOK(t *testing.T) {
	a, err := newAllowlist(filepath.Join(t.TempDir(), "nope.json"), time.Hour)
	if err != nil || len(a.m) != 0 {
		t.Errorf("missing file: %v %v", a, err)
	}
}

func TestAllowlistCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	for _, body := range []string{`{not json`, `{"not-an-ip":"2026-09-27T01:00:00Z"}`, `{"203.0.113.9":"soon"}`} {
		os.WriteFile(path, []byte(body), 0o600)
		if _, err := newAllowlist(path, time.Hour); err == nil {
			t.Errorf("%s: no error", body)
		}
	}
}
