# Testing

[Developer guide](README.md) > Testing

```sh
GOTOOLCHAIN=auto go test -race ./...
```

Tests are plain `testing` package tests in `package main`, with no helpers
from outside the standard library. They use `net/http/httptest` for handlers
and injected clocks for time. There is no test for `main.go` and no test that
talks to a real SMTP server. Anything that writes the state file writes it
under `t.TempDir()`, never `/data`.

## Layout

| File | Covers |
|------|--------|
| `config_test.go` | `loadConfig`, `applyEnv`, `validate` |
| `tokens_test.go` | `tokenStore` and `limiter` |
| `allow_test.go` | `allowlist` (`newAllowlist`, `add`, `allowed`, `purge`, `save`) and `publicIPv4` |
| `mail_test.go` | `composeMessage` |
| `handlers_test.go` | All routes through `(*server).routes()`, including middleware and static file |

## The allowlist in tests

There is no fake to install. The allowlist is a real `*allowlist` pointed at a
file in a per-test temporary directory, so tests exercise the same load, save
and rename code that production does, and clean up automatically.

- **`newTestAllowlist(t) (*allowlist, *fakeClock, string)`** (in
  `allow_test.go`) creates one at `filepath.Join(t.TempDir(), "allowlist.json")`
  with a 72 h TTL, swaps `a.now` for a `fakeClock` (from `tokens_test.go`), and
  returns the list, the clock and the path. Advance time with
  `clock.t = clock.t.Add(...)`.
- **`writeState(t, body) string`** writes a JSON body to a fresh temp path and
  returns it; call `newAllowlist(path, ttl)` on it to test loading
  (`TestAllowlistLoadDropsExpiredAndNonPublic`,
  `TestAllowlistCorruptFileMovedAside`). Loading purges against the real clock,
  so build expiries from `time.Now()` there.
- To test a failed save, `os.Chmod` the temp directory to `0500` (and back in
  `t.Cleanup`). Such tests skip when run as root, which ignores directory
  permissions.
- To check what was persisted, read the file (`os.ReadFile`) or build a second
  `newAllowlist` from the same path (`TestAllowlistPersistsAcrossRestart`).

Each test gets its own directory, so there is no shared state; the v0.1 rule
against `t.Parallel()` (a global `runIpset`) no longer applies, though no test
uses it today.

## Handler tests

`handlers_test.go` provides two helpers:

- **`newTestServer(t, proxies...) (*server, http.Handler, chan sentMail)`**
  builds a `Config` literal directly (bypassing `loadConfig` and `validate`, so
  it sets the unexported `proxies` itself from the optional CIDR strings,
  default `127.0.0.1/32` and `::1/128`), sets `StateFile` to
  `filepath.Join(t.TempDir(), "allowlist.json")`, builds a real allowlist with
  `newAllowlist`, and passes `newServer` a fake sender that pushes
  `sentMail{to, link}` onto a buffered channel. It returns the server (for
  inspecting `s.tokens`, `s.allow.m` and `s.cfg.StateFile`), the full handler
  from `s.routes()` (so middleware runs), and the channel.
- **`mails(sent) int`** drains the channel until 100 ms pass with nothing, and
  returns the count; used by the rate-limit tests.
- **`do(h, method, target, remote, form, xff...)`** builds an `httptest`
  request, form-encodes `form` if non-nil, sets `RemoteAddr` if `remote` is
  non-empty (the `httptest` default is `192.0.2.1:1234`), adds one
  `X-Forwarded-For` header line per `xff` argument, and returns the recorder.

Because mail is sent in a goroutine, tests read the channel with a timeout:

```go
select {
case m := <-sent:
    // assert on m.to and m.link
case <-time.After(2 * time.Second):
    t.Fatal("no mail sent")
}
```

To test "no mail sent", wait briefly and fail if anything arrives
(`TestRequestRateLimitedLooksIdentical` uses 100 ms).

Tokens for `/verify` tests are minted directly with `s.tokens.issue(...)`
rather than going through `/request`.

Two more helpers serve the `/check` tests:

- **`unlock(t, s, h, remote, xff...)`** mints a token and posts it to
  `/verify` from `remote` (with optional `X-Forwarded-For`), failing the test
  unless it gets 200. After it, that address is on the allowlist.
- **`assertLocked(t, w, msg)`** checks a response is the 403 locked page: full
  layout, contains `msg`, and links to the portal URL.

### Testing `/check`

Drive it like Caddy does: `GET /check` from a trusted proxy address with the
client in `X-Forwarded-For`, or directly from `remote` for a peer with no
proxy. Assert `200`, body `ok` and `Content-Type: text/plain` for an unlocked
address, and `assertLocked` otherwise:

```go
func TestCheckAfterUnlockViaCompose(t *testing.T) {
    s, h, _ := newTestServer(t, "172.28.0.0/24")
    assertLocked(t, do(h, "GET", "/check", "172.28.0.3:4444", nil, "203.0.113.5"), "not unlocked")
    unlock(t, s, h, "172.28.0.3:4444", "203.0.113.5")
    if w := do(h, "GET", "/check", "172.28.0.3:4444", nil, "203.0.113.5"); w.Code != 200 {
        t.Fatalf("%d %s", w.Code, w.Body)
    }
}
```

For expiry through `/check`, set `s.allow.now` to a `fakeClock` before
unlocking and advance it past the TTL.

## What each test checks

### `config_test.go`

Helpers: `envKeys` (every `TYC_*` key), `clearEnv` (unsets them all, restored
by `t.Setenv` on cleanup), `setRequiredEnv`, `writeConfig` (temp file and
`TYC_CONFIG`), `fileConfig` (sample JSON).

| Test | Checks |
|------|--------|
| `TestLoadConfigEnvOnly` | Defaults applied, including `StateFile`; emails lower-cased and trimmed; two default proxies parsed |
| `TestLoadConfigFileOnly` | JSON values loaded, including nested SMTP port and a duration |
| `TestLoadConfigEnvOverridesFile` | Env wins for set keys; file value kept for unset ones |
| `TestLoadConfigMissingRequiredNamesField` | Each of six required keys, when unset, produces an error naming it |
| `TestLoadConfigMalformedDuration` | Bad duration in env names the key; bad duration in the file is reported |
| `TestLoadConfigMalformedValues` | Bad port, zero rate, bad proxy, relative URL and bad From each name their key |
| `TestLoadConfigTrustedProxiesCIDRAndBare` | A mixed list of CIDR, bare IPv4 and bare IPv6 (with spaces) parses to the expected prefixes; `/33` is rejected naming the key |
| `TestLoadConfigIpsetNameRejected` | `TYC_IPSET_NAME` set, even empty, fails with the exact migration message |
| `TestLoadConfigStateFile` | `TYC_STATE_FILE` overrides the default; an empty value is a required-field error |
| `TestLoadConfigPublicURLMustBeHTTPS` | `https://` accepted; `http://` refused except for `localhost` and `127.0.0.1` |
| `TestLoadConfigTrustedEmailsMustBeBareAddresses` | A bare word, a display-name form and an address with an empty local part are each refused, naming the key |

### `tokens_test.go`

Uses a `fakeClock` whose `now` method is assigned to `s.now` / `l.now`.

| Test | Checks |
|------|--------|
| `TestTokenIssuePeekConsume` | Token is 32 bytes of base64url; peek is repeatable; consume works once; peek fails after consume; unknown token fails |
| `TestTokenStoresOnlyHash` | Raw token is not a map key |
| `TestTokenExpiry` | After TTL, peek and consume fail and the map is empty (purged) |
| `TestLimiterSlidingWindow` | Three allowed, fourth denied; keys isolated; one slot frees exactly when the oldest leaves the window |

### `allow_test.go`

| Test | Checks |
|------|--------|
| `TestAllowlistAddThenAllowed` | Not allowed before `add`; allowed after; another address still not allowed |
| `TestAllowlistExpiry` | Allowed one second before the TTL, not at it; the expired entry is purged from the map |
| `TestAllowlistRefreshExtendsExpiry` | Re-adding after 48 h keeps the address allowed 96 h after the first add |
| `TestAllowlistOneAddressPerEmail` | Adding a second address for the same email drops and returns the first; other emails' addresses are untouched |
| `TestAllowlistSameAddressLatestEmailWins` | Two emails adding one address leave one entry, under the later email, with the later expiry |
| `TestAllowlistRefusesNonPublicIPv4` | Private, loopback, link-local, multicast, unspecified, `240.0.0.0/4`, broadcast, IPv6, IPv4-mapped IPv6 and zero `Addr` are refused; nothing stored and no state file written |
| `TestAllowlistPersistsAcrossRestart` | A second `newAllowlist` on the same path sees the entry and its email |
| `TestAllowlistFileFormat` | The exact JSON written: `{"<ip>":{"email":...,"expires":"<RFC 3339>"}}` |
| `TestAllowlistLoadDropsExpiredAndNonPublic` | A file with an expired, a private and a valid entry loads only the valid one |
| `TestAllowlistFileModeAndNoTempLeft` | State file mode is `0600`; no temp file left in the directory |
| `TestAllowlistSaveFailureLeavesMemoryUnchanged` | With the directory read-only, `add` fails, memory is unchanged and no temp file is left |
| `TestAllowlistMissingFileOK` | A missing file gives an empty list and no error |
| `TestAllowlistCorruptFileMovedAside` | Invalid JSON, a non-address key, a bad time and the old address-to-string shape each load as empty, with the original moved to `.corrupt` |

### `mail_test.go`

| Test | Checks |
|------|--------|
| `TestComposeMessage` | CRLF line endings; parses with `net/mail`; From, To, Subject, Date, Message-ID, MIME headers; body contains the link |
| `TestMessageID` | 32 hex characters at the From domain; two calls differ |

`sendMagicLink` has no unit test; it is exercised manually against a real
account.

### `handlers_test.go`

| Test | Checks |
|------|--------|
| `TestIndexRendersFormWithSecurityHeaders` | Form markup; every security header (CSP, HSTS, nosniff, referrer, cache) and Content-Type; 404 for unknown path; 405 for `POST /` |
| `TestRequestSameResponseForKnownAndUnknown` | Identical status and body; mail sent only for the known (normalised) address with the right link; exactly one token minted |
| `TestRequestRateLimitedLooksIdentical` | Five requests give identical 200 pages; only three mails sent |
| `TestRequestLimitPerEmailAndIP` | Four requests from one address send three mails; a request from a second address still sends one |
| `TestRequestPerEmailCapAcrossIPs` | Three requests from each of five addresses send twelve mails, the per-email cap |
| `TestRequestFromBadIPDoesNoWork` | Requests from IPv6 and private peers get the same page as an unknown email, send no mail and mint no token |
| `TestRequestBodyTooLarge` | 5000-byte form gives 400 |
| `TestVerifyXFFIgnoredFromUntrustedRemote` | XFF from a non-proxy peer ignored; peer address shown |
| `TestVerifyXFFLastHopHonouredFromTrustedProxy` | Two XFF lines from loopback; only the last hop of the last line is used |
| `TestVerifyRejectsIPv6AndPrivate` | IPv6 peer, private peer, private/IPv6/garbage XFF: GET and POST both 400 with the IPv6 message; allowlist unchanged; token not consumed |
| `TestVerifyGetDoesNotConsume` | Two GETs show the form with the hidden token; token still valid; bogus token 400 |
| `TestVerifyPostAddsOnceThenRejectsReuse` | First POST 200 with the address; second 400; the address is in the state file |
| `TestVerifyPostSaveFailureKeepsToken` | With the state directory read-only, POST gives 500 and the token survives; after restoring permissions the same token succeeds |
| `TestStaticCSS` | Both stylesheets 200 with `text/css`, immutable cache header, security headers present |
| `TestCheckLockedUntilVerified` | 403 locked page before unlock; 200 `ok` `text/plain` after, from a different source port; other addresses still locked |
| `TestCheckViaTrustedProxy` | Unlocked address via XFF from loopback gets 200; IPv6, private and garbage XFF get the locked page with the IPv6 message |
| `TestCheckXFFIgnoredFromUntrustedRemote` | An unlocked address in XFF from an untrusted peer does not unlock the peer |
| `TestCheckCIDRTrustedProxy` | With `172.28.0.0/24` trusted, a peer inside it is believed; a peer in `172.29.0.0/24` is not |

## Adding a handler test

1. Start from `newTestServer(t)`, or `newTestServer(t, "172.28.0.0/24")` to
   act like the shipped compose network. Keep `s` if you need the token store
   or the allowlist.
2. Mint tokens with `s.tokens.issue("alice@example.com")`, or unlock an
   address in one step with `unlock(t, s, h, remote, xff...)`.
3. Drive the handler with `do(h, method, path, remote, form, xff...)`. Use
   public test addresses from `203.0.113.0/24` or `198.51.100.0/24` for
   `remote` and `xff`; use `127.0.0.1:<port>` (or an address in the prefix you
   passed) as `remote` to act as the trusted proxy.
4. Assert on `w.Code`, `w.Body.String()` and `w.Header()`, on `s.allow.m` or
   the file at `s.cfg.StateFile`, and on the `sent` channel with a timeout.

Example:

```go
func TestVerifyPostUsesProxyHop(t *testing.T) {
    s, h, _ := newTestServer(t)
    tok, _ := s.tokens.issue("alice@example.com")
    w := do(h, "POST", "/verify", "127.0.0.1:4444", url.Values{"token": {tok}}, "198.51.100.7")
    if w.Code != 200 || !s.allow.allowed(netip.MustParseAddr("198.51.100.7")) {
        t.Fatalf("%d %v", w.Code, s.allow.m)
    }
}
```

For config changes, see
[Adding a new setting](configuration-internals.md#adding-a-new-setting).
