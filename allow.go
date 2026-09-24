package main

import (
	"encoding/json"
	"errors"
	"log"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type allowEntry struct {
	Email   string    `json:"email"`
	Expires time.Time `json:"expires"`
}

// allowlist maps unlocked public IPv4 addresses to the email that unlocked
// them and their expiry, persisted to a JSON file so unlocks survive restarts.
type allowlist struct {
	mu   sync.Mutex
	path string
	ttl  time.Duration
	now  func() time.Time
	m    map[netip.Addr]allowEntry
}

// newAllowlist loads path if it exists, dropping expired and non-public
// entries. A file that fails to parse is moved to path+".corrupt".
func newAllowlist(path string, ttl time.Duration) (*allowlist, error) {
	a := &allowlist{path: path, ttl: ttl, now: time.Now, m: map[netip.Addr]allowEntry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[netip.Addr]allowEntry{}
	if err := json.Unmarshal(b, &m); err != nil {
		log.Printf("allowlist %s: %v; moving it to %s.corrupt and starting empty", path, err, path)
		return a, os.Rename(path, path+".corrupt")
	}
	maps.DeleteFunc(m, func(ip netip.Addr, _ allowEntry) bool { return !publicIPv4(ip) })
	a.m = m
	a.purge()
	return a, nil
}

// add unlocks ip for email until now+ttl. An email holds at most one address:
// any other address it held is dropped and returned as replaced. An address
// is a single key, so if two emails unlock the same one the latest add wins.
// Memory changes only once the new state is saved.
func (a *allowlist) add(ip netip.Addr, email string) (replaced netip.Addr, err error) {
	if !publicIPv4(ip) {
		return netip.Addr{}, errors.New("refusing to add non-public or non-IPv4 address")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purge()
	next := maps.Clone(a.m)
	for old, e := range next {
		if e.Email == email && old != ip {
			delete(next, old)
			replaced = old
		}
	}
	next[ip] = allowEntry{email, a.now().Add(a.ttl).UTC().Truncate(time.Second)}
	if err := a.save(next); err != nil {
		return netip.Addr{}, err
	}
	a.m = next
	return replaced, nil
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
	maps.DeleteFunc(a.m, func(_ netip.Addr, e allowEntry) bool { return !now.Before(e.Expires) })
}

// save writes m to the state file via a synced temp file, rename and
// directory sync. Caller holds a.mu.
func (a *allowlist) save(m map[netip.Addr]allowEntry) (err error) {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.path)
	f, err := os.CreateTemp(dir, ".allowlist-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o600); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), a.path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// reserved is 240.0.0.0/4, which IsGlobalUnicast accepts apart from broadcast.
var reserved = netip.MustParsePrefix("240.0.0.0/4")

func publicIPv4(ip netip.Addr) bool {
	return ip.Is4() && ip.IsGlobalUnicast() && !reserved.Contains(ip) && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}
