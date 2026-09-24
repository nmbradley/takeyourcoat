package main

import (
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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

func writeState(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAllowlistAddThenAllowed(t *testing.T) {
	a, _, _ := newTestAllowlist(t)
	if a.allowed(visitor) {
		t.Fatal("allowed before add")
	}
	if old, err := a.add(visitor, "alice@example.com"); err != nil || old.IsValid() {
		t.Fatal(old, err)
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
	a.add(visitor, "alice@example.com")
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
	a.add(visitor, "alice@example.com")
	clock.t = clock.t.Add(48 * time.Hour)
	if old, _ := a.add(visitor, "alice@example.com"); old.IsValid() {
		t.Errorf("refresh reported replacing %s", old)
	}
	clock.t = clock.t.Add(48 * time.Hour)
	if !a.allowed(visitor) {
		t.Error("refresh did not extend expiry")
	}
}

func TestAllowlistOneAddressPerEmail(t *testing.T) {
	a, _, _ := newTestAllowlist(t)
	other := netip.MustParseAddr("198.51.100.7")
	a.add(visitor, "alice@example.com")
	a.add(netip.MustParseAddr("192.0.2.44"), "bob@example.com")
	old, err := a.add(other, "alice@example.com")
	if err != nil || old != visitor {
		t.Fatalf("replaced = %v, %v; want %s", old, err, visitor)
	}
	if a.allowed(visitor) || !a.allowed(other) || !a.allowed(netip.MustParseAddr("192.0.2.44")) {
		t.Errorf("after replace: %v", a.m)
	}
}

func TestAllowlistSameAddressLatestEmailWins(t *testing.T) {
	a, clock, _ := newTestAllowlist(t)
	a.add(visitor, "alice@example.com")
	clock.t = clock.t.Add(time.Hour)
	a.add(visitor, "bob@example.com")
	if e := a.m[visitor]; len(a.m) != 1 || e.Email != "bob@example.com" || !e.Expires.Equal(clock.t.Add(72*time.Hour)) {
		t.Errorf("entry = %+v", a.m)
	}
}

func TestAllowlistRefusesNonPublicIPv4(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	for _, s := range []string{
		"10.0.0.1", "172.16.5.5", "192.168.1.1", "127.0.0.1", "169.254.1.1",
		"224.0.0.1", "0.0.0.0", "240.0.0.1", "255.255.255.255",
		"::1", "2001:db8::1", "::ffff:203.0.113.9", "fe80::1",
	} {
		if _, err := a.add(netip.MustParseAddr(s), "alice@example.com"); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if _, err := a.add(netip.Addr{}, "alice@example.com"); err == nil {
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
	if _, err := a.add(visitor, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	b, err := newAllowlist(path, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !b.allowed(visitor) || b.m[visitor].Email != "alice@example.com" {
		t.Errorf("entry lost across restart: %v", b.m)
	}
}

func TestAllowlistFileFormat(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	a.add(visitor, "alice@example.com")
	b, _ := os.ReadFile(path)
	want := `{"203.0.113.9":{"email":"alice@example.com","expires":"` +
		time.Unix(1_000_000, 0).Add(72*time.Hour).UTC().Format(time.RFC3339) + `"}}`
	if string(b) != want {
		t.Errorf("file = %s\nwant   %s", b, want)
	}
}

func TestAllowlistLoadDropsExpiredAndNonPublic(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	path := writeState(t, `{"203.0.113.9":{"email":"a@x","expires":"`+past+`"},`+
		`"10.0.0.1":{"email":"b@x","expires":"`+future+`"},`+
		`"198.51.100.7":{"email":"c@x","expires":"`+future+`"}}`)
	a, err := newAllowlist(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.m) != 1 || !a.allowed(netip.MustParseAddr("198.51.100.7")) {
		t.Errorf("load kept %v", a.m)
	}
}

func TestAllowlistFileModeAndNoTempLeft(t *testing.T) {
	a, _, path := newTestAllowlist(t)
	if _, err := a.add(visitor, "alice@example.com"); err != nil {
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

func TestAllowlistSaveFailureLeavesMemoryUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	a, _, path := newTestAllowlist(t)
	a.add(visitor, "alice@example.com")
	before := maps.Clone(a.m)
	dir := filepath.Dir(path)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := a.add(netip.MustParseAddr("198.51.100.7"), "alice@example.com"); err == nil {
		t.Fatal("add succeeded in read-only dir")
	}
	if !maps.Equal(a.m, before) {
		t.Errorf("memory changed on failed save: %v, want %v", a.m, before)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestAllowlistMissingFileOK(t *testing.T) {
	a, err := newAllowlist(filepath.Join(t.TempDir(), "nope.json"), time.Hour)
	if err != nil || len(a.m) != 0 {
		t.Errorf("missing file: %v %v", a, err)
	}
}

func TestAllowlistCorruptFileMovedAside(t *testing.T) {
	for _, body := range []string{
		`{not json`,
		`{"not-an-ip":{"email":"a@x","expires":"2026-09-27T01:00:00Z"}}`,
		`{"203.0.113.9":{"email":"a@x","expires":"soon"}}`,
		`{"203.0.113.9":"2026-09-27T01:00:00Z"}`,
	} {
		path := writeState(t, body)
		a, err := newAllowlist(path, time.Hour)
		if err != nil || len(a.m) != 0 {
			t.Errorf("%s: %v %v", body, a, err)
			continue
		}
		moved, err := os.ReadFile(path + ".corrupt")
		if err != nil || !strings.Contains(string(moved), body) {
			t.Errorf("%s: not moved aside: %q %v", body, moved, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: corrupt file still in place", body)
		}
	}
}
