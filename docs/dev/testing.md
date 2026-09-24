# Testing

[Developer guide](README.md) > Testing

```sh
GOTOOLCHAIN=auto go test -race ./...
```

Tests are plain `testing` package tests in `package main`, with no helpers
from outside the standard library. They use `net/http/httptest` for handlers
and injected clocks for time. There is no test for `main.go` and no test that
talks to a real SMTP server or a real ipset.

## Layout

| File | Covers |
|------|--------|
| `config_test.go` | `loadConfig`, `applyEnv`, `validate` |
| `tokens_test.go` | `tokenStore` and `limiter` |
| `ipset_test.go` | `TestMain`, the `fakeIpset` helper, `ipsetAdd`, `publicIPv4` |
| `mail_test.go` | `composeMessage` |
| `handlers_test.go` | All routes through `(*server).routes()`, including middleware and static file |

## The `runIpset` fake

`ipset.go` declares `runIpset` as a package-level `var` holding a function, so
tests can swap it. Two layers keep the real binary out of reach:

1. **`TestMain`** (in `ipset_test.go`) replaces `runIpset` before any test runs
   with a function that always returns `real ipset disabled in tests`. Any test
   that reaches ipset without asking for a fake gets an error, not a host
   change.
2. **`fakeIpset(t) *[][]string`** installs a recorder that appends each argv
   and returns nil, and restores the previous function with `t.Cleanup`. Tests
   assert on the recorded calls:

   ```go
   calls := fakeIpset(t)
   // ... exercise code ...
   want := []string{"add", "jellyfin_clients", "203.0.113.9", "timeout", "259200", "-exist"}
   if len(*calls) != 1 || !slices.Equal((*calls)[0], want) { ... }
   ```

Because `runIpset` is global, tests that use `fakeIpset` must not call
`t.Parallel()`. None do.

## Handler tests

`handlers_test.go` provides two helpers:

- **`newTestServer(t) (*server, http.Handler, chan sentMail)`** builds a
  `Config` literal directly (bypassing `loadConfig` and `validate`, so it sets
  the unexported `proxies` itself), and passes `newServer` a fake sender that
  pushes `sentMail{to, link}` onto a buffered channel. It returns the server
  (for inspecting `s.tokens`), the full handler from `s.routes()` (so middleware
  runs), and the channel.
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

## What each test checks

### `config_test.go`

Helpers: `envKeys` (every `TYC_*` key), `clearEnv` (unsets them all, restored
by `t.Setenv` on cleanup), `setRequiredEnv`, `writeConfig` (temp file and
`TYC_CONFIG`), `fileConfig` (sample JSON).

| Test | Checks |
|------|--------|
| `TestLoadConfigEnvOnly` | Defaults applied; emails lower-cased and trimmed; two default proxies parsed |
| `TestLoadConfigFileOnly` | JSON values loaded, including nested SMTP port and a duration |
| `TestLoadConfigEnvOverridesFile` | Env wins for set keys; file value kept for unset ones |
| `TestLoadConfigMissingRequiredNamesField` | Each of six required keys, when unset, produces an error naming it |
| `TestLoadConfigMalformedDuration` | Bad duration in env names the key; bad duration in the file is reported |
| `TestLoadConfigMalformedValues` | Bad port, zero rate, bad proxy, relative URL and bad From each name their key |

### `tokens_test.go`

Uses a `fakeClock` whose `now` method is assigned to `s.now` / `l.now`.

| Test | Checks |
|------|--------|
| `TestTokenIssuePeekConsume` | Token is 32 bytes of base64url; peek is repeatable; consume works once; peek fails after consume; unknown token fails |
| `TestTokenStoresOnlyHash` | Raw token is not a map key |
| `TestTokenExpiry` | After TTL, peek and consume fail and the map is empty (purged) |
| `TestLimiterSlidingWindow` | Three allowed, fourth denied; keys isolated; one slot frees exactly when the oldest leaves the window |

### `ipset_test.go`

| Test | Checks |
|------|--------|
| `TestIpsetAddArgv` | Exact argv for 72 h: `add jellyfin_clients 203.0.113.9 timeout 259200 -exist` |
| `TestIpsetAddRefusesNonPublicIPv4` | Private, loopback, link-local, multicast, unspecified, IPv6, IPv4-mapped IPv6 and zero `Addr` are refused and ipset is never called |

### `mail_test.go`

| Test | Checks |
|------|--------|
| `TestComposeMessage` | CRLF line endings; parses with `net/mail`; From, To, Subject, Date, MIME headers; body contains the link |

`sendMagicLink` has no unit test; it is exercised manually against a real
account.

### `handlers_test.go`

| Test | Checks |
|------|--------|
| `TestIndexRendersFormWithSecurityHeaders` | Form markup; all four security headers and Content-Type; 404 for unknown path; 405 for `POST /` |
| `TestRequestSameResponseForKnownAndUnknown` | Identical status and body; mail sent only for the known (normalised) address with the right link; exactly one token minted |
| `TestRequestRateLimitedLooksIdentical` | Five requests give identical 200 pages; only three mails sent |
| `TestRequestBodyTooLarge` | 5000-byte form gives 400 |
| `TestVerifyXFFIgnoredFromUntrustedRemote` | XFF from a non-proxy peer ignored; peer address shown |
| `TestVerifyXFFLastHopHonouredFromTrustedProxy` | Two XFF lines from loopback; only the last hop of the last line is used |
| `TestVerifyRejectsIPv6AndPrivate` | IPv6 peer, private peer, private/IPv6/garbage XFF: GET and POST both 400 with the IPv6 message; ipset not called; token not consumed |
| `TestVerifyGetDoesNotConsume` | Two GETs show the form with the hidden token; token still valid; bogus token 400 |
| `TestVerifyPostAddsOnceThenRejectsReuse` | First POST 200 with the address; second 400; exactly one ipset call with the exact argv |
| `TestStaticCSS` | 200, `text/css`, immutable cache header, security headers present |

## Adding a handler test

1. Start from `newTestServer(t)`. Keep `s` if you need the token store.
2. If the path can reach ipset, call `calls := fakeIpset(t)` first (otherwise
   the `TestMain` stub makes `ipsetAdd` fail with a 500).
3. Mint tokens with `s.tokens.issue("alice@example.com")`.
4. Drive the handler with `do(h, method, path, remote, form, xff...)`. Use
   public test addresses from `203.0.113.0/24` or `198.51.100.0/24` for
   `remote` and `xff`; use `127.0.0.1:<port>` as `remote` to act as the trusted
   proxy.
5. Assert on `w.Code`, `w.Body.String()` and `w.Header()`, on `*calls`, and on
   the `sent` channel with a timeout.
6. Do not call `t.Parallel()`.

Example:

```go
func TestVerifyPostUsesProxyHop(t *testing.T) {
    s, h, _ := newTestServer(t)
    calls := fakeIpset(t)
    tok, _ := s.tokens.issue("alice@example.com")
    w := do(h, "POST", "/verify", "127.0.0.1:4444", url.Values{"token": {tok}}, "198.51.100.7")
    if w.Code != 200 || len(*calls) != 1 || (*calls)[0][2] != "198.51.100.7" {
        t.Fatalf("%d %q", w.Code, *calls)
    }
}
```

For config changes, see
[Adding a new setting](configuration-internals.md#adding-a-new-setting).
