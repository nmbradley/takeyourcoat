package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"time"
)

// runIpset execs ipset with a fixed argv and no shell. Tests replace it.
var runIpset = func(args ...string) error {
	out, err := exec.Command("ipset", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ipset %v: %w: %s", args, err, bytes.TrimSpace(out))
	}
	return nil
}

func ipsetAdd(set string, ip netip.Addr, ttl time.Duration) error {
	if !publicIPv4(ip) {
		return errors.New("refusing to add non-public or non-IPv4 address")
	}
	secs := strconv.FormatInt(int64(ttl/time.Second), 10)
	return runIpset("add", set, ip.String(), "timeout", secs, "-exist")
}

func publicIPv4(ip netip.Addr) bool {
	return ip.Is4() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}
