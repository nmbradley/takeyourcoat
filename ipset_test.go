package main

import (
	"errors"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"
)

// TestMain guarantees no test ever execs the real ipset binary.
func TestMain(m *testing.M) {
	runIpset = func(args ...string) error { return errors.New("real ipset disabled in tests") }
	os.Exit(m.Run())
}

func fakeIpset(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := runIpset
	runIpset = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { runIpset = orig })
	return &calls
}

func TestIpsetAddArgv(t *testing.T) {
	calls := fakeIpset(t)
	if err := ipsetAdd("jellyfin_clients", netip.MustParseAddr("203.0.113.9"), 72*time.Hour); err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "jellyfin_clients", "203.0.113.9", "timeout", "259200", "-exist"}
	if len(*calls) != 1 || !slices.Equal((*calls)[0], want) {
		t.Errorf("argv = %q, want one call with %q", *calls, want)
	}
}

func TestIpsetAddRefusesNonPublicIPv4(t *testing.T) {
	calls := fakeIpset(t)
	for _, s := range []string{
		"10.0.0.1", "172.16.5.5", "192.168.1.1", "127.0.0.1", "169.254.1.1",
		"224.0.0.1", "0.0.0.0", "::1", "2001:db8::1", "::ffff:203.0.113.9", "fe80::1",
	} {
		if err := ipsetAdd("set", netip.MustParseAddr(s), time.Hour); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if err := ipsetAdd("set", netip.Addr{}, time.Hour); err == nil {
		t.Error("zero Addr accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("ipset called for refused addresses: %q", *calls)
	}
}
