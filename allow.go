package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"
)

// allowlist maps unlocked public IPv4 addresses to their expiry and persists
// them to a JSON file so unlocks survive restarts.
type allowlist struct {
	mu   sync.Mutex
	path string
	ttl  time.Duration
	now  func() time.Time
	m    map[netip.Addr]time.Time
}

// newAllowlist loads path if it exists. Expired entries are dropped.
func newAllowlist(path string, ttl time.Duration) (*allowlist, error) {
	a := &allowlist{path: path, ttl: ttl, now: time.Now, m: map[netip.Addr]time.Time{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &a.m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	a.purge()
	return a, nil
}

// add unlocks ip until now+ttl, refreshing any existing expiry.
func (a *allowlist) add(ip netip.Addr) error {
	if !publicIPv4(ip) {
		return errors.New("refusing to add non-public or non-IPv4 address")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purge()
	a.m[ip] = a.now().Add(a.ttl).UTC().Truncate(time.Second)
	return a.save()
}

func (a *allowlist) allowed(ip netip.Addr) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purge()
	_, ok := a.m[ip]
	return ok
}

// purge drops expired entries. Caller holds a.mu.
func (a *allowlist) purge() {
	now := a.now()
	for ip, exp := range a.m {
		if !now.Before(exp) {
			delete(a.m, ip)
		}
	}
}

// save writes the state file atomically. Caller holds a.mu.
func (a *allowlist) save() error {
	b, err := json.Marshal(a.m)
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

func publicIPv4(ip netip.Addr) bool {
	return ip.Is4() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}
