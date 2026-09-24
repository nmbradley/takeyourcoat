# Security design

[Developer guide](README.md) > Security design

Each control, where it lives, and what to preserve when changing it. The
threat model for operators is in [the security model](../user/security-model.md).

## Summary

| Control | Location |
|---------|----------|
| Trusted-proxy `X-Forwarded-For` handling | `handlers.go:clientIP`, `config.go:validate` |
| Public IPv4 check | `ipset.go:publicIPv4`, called from `clientIP` and `ipsetAdd` |
| Fixed argv exec, no shell | `ipset.go:runIpset`, `ipset.go:ipsetAdd` |
| Token generation, hashing, single use, expiry | `tokens.go:issue`, `peek`, `consume`, `purge` |
| Anti-enumeration | `handlers.go:handleRequest` |
| Rate limiting | `tokens.go:limiter.allow`, `handlers.go:newServer` |
| Form size limit | `handlers.go:handleRequest`, `handleVerifyPost` |
| Server timeouts | `main.go:main` |
| CSP and headers | `handlers.go:securityHeaders`, `main.go:routes` |
| Output escaping | `handlers.go:pages` (`html/template`) |
| STARTTLS fails closed | `mail.go:sendMagicLink` |
| Config validation naming the field | `config.go:applyEnv`, `config.go:validate` |
| No secrets logged | all `log.Printf` call sites |

## Trusted-proxy `X-Forwarded-For` handling

`handlers.go:clientIP`:

1. Parse `r.RemoteAddr` with `netip.ParseAddrPort`; failure means not ok.
2. `Unmap()` it, so an IPv4-mapped IPv6 peer (`::ffff:127.0.0.1`) compares
   equal to `127.0.0.1`.
3. Only if the request has at least one `X-Forwarded-For` header **and** the
   peer is in `cfg.proxies`: take the **last** header value, split on commas,
   take the **last** hop, trim, `netip.ParseAddr`, `Unmap()`. A parse failure
   means not ok; it never falls back to the peer address.
4. Return the address and `publicIPv4(address)`.

Why the last hop of the last header: each proxy appends the address it
received the connection from. Everything to the left was supplied by the
client or by proxies we do not control, so only the rightmost entry, the one
our trusted proxy wrote, is trustworthy. Multiple `X-Forwarded-For` header
lines arrive as separate entries in `r.Header.Values`, and the last line is the
one appended last. `TestVerifyXFFLastHopHonouredFromTrustedProxy` sends two
header lines with a spoofed left side and checks only the final hop is used.

From an untrusted peer, `X-Forwarded-For` is ignored entirely
(`TestVerifyXFFIgnoredFromUntrustedRemote`).

`cfg.proxies` is built in `config.go:validate` from `TrustedProxies`: trimmed,
empty entries skipped, parsed with `netip.ParseAddr` (exact addresses only, no
CIDR), and `Unmap()`ed. The default is `127.0.0.1` and `::1`.

## Public IPv4 check

`ipset.go:publicIPv4` returns true only if the address is `Is4()` and not
`IsPrivate`, `IsLoopback`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`,
`IsMulticast` or `IsUnspecified`. `Is4()` is false for IPv4-mapped IPv6, which
is why callers `Unmap()` first. The zero `netip.Addr` fails `Is4()`.

It is applied twice: in `clientIP` (so handlers reject early, before touching a
token) and again in `ipsetAdd` (so no future caller can pass a bad address to
ipset). `TestIpsetAddRefusesNonPublicIPv4` covers RFC 1918, loopback,
link-local, multicast, `0.0.0.0`, IPv6, mapped IPv6 and the zero value.

Note that `IsPrivate` does not cover carrier-grade NAT space (`100.64.0.0/10`)
or documentation ranges; those are treated as public. In practice the portal
only ever sees the ISP's public egress address.

## Fixed argv exec, no shell

`ipset.go:runIpset` is `exec.Command("ipset", args...).CombinedOutput()`. There
is no shell, so no quoting or metacharacter interpretation. `ipsetAdd` always
builds:

```text
add <cfg.IpsetName> <ip.String()> timeout <seconds> -exist
```

- `ip.String()` is the canonical form of a parsed `netip.Addr`; request text
  never reaches argv.
- `<seconds>` is `strconv.FormatInt(int64(ttl/time.Second), 10)`.
- `-exist` makes re-adding an existing entry succeed and reset its timeout.
- `IpsetName` comes from operator config and is only checked non-empty.

`ipset` is resolved through `PATH`; in the image that is `/usr/sbin/ipset`,
on a read-only root filesystem. `runIpset` is a package variable so tests can
replace it ([Testing](testing.md)).

## Tokens

`tokens.go`:

| Property | Implementation |
|----------|----------------|
| Generation | `issue`: 32 bytes from `crypto/rand.Read`, encoded with `base64.RawURLEncoding` (43 URL-safe characters, no padding). |
| Storage | Only `sha256.Sum256(token)` is stored, as a `[32]byte` map key, with `{email, expiry}`. A memory dump or debug print does not reveal usable links (`TestTokenStoresOnlyHash`). |
| Lookup | Hash the presented token, then a map lookup. The lookup is on the hash, so there is no byte-by-byte comparison of secrets to time. |
| Single use | `consume` deletes the entry under the same lock as the lookup, so two concurrent POSTs cannot both succeed. `peek` (used by `GET /verify`) does not delete. |
| Expiry | `expiry = now + TokenTTL`. `purge` runs at the start of `issue`, `peek` and `consume` and deletes every entry with `!now.Before(expiry)`, so an expired token is never returned and does not linger. |
| Scope | A token records the email but not the client address; the address is taken from the POST. |

Tokens are in memory only and vanish on restart.

## Anti-enumeration

`handlers.go:handleRequest` renders the identical `sent` page with status 200
for:

- a trusted address that gets a link,
- a trusted address that is rate limited,
- an unknown address,
- a trusted address where token generation failed.

For unknown addresses it does no work: `s.trusted[email] && s.limiter.allow(email)`
short-circuits, so no limiter entry and no token are created
(`TestRequestSameResponseForKnownAndUnknown` checks the token map). Sending runs
in a goroutine, so SMTP latency does not show in the response time. The
remaining difference, one token generation for trusted addresses, is
microseconds.

`GET /verify` and `POST /verify` also give one message for "unknown", "used"
and "expired" tokens.

## Rate limiter

`tokens.go:limiter.allow(key)` is a sliding-window log:

1. Lock.
2. Keep only timestamps for `key` with `now.Sub(t) < window`, reusing the
   slice's backing array.
3. If `len(recent) >= limit`, store the trimmed slice and deny. A denied
   request is **not** recorded, so hammering does not extend the lockout.
4. Otherwise append `now`, store, allow.

`newServer` builds it with `limit = RequestsPerEmailPerHour` and a fixed
`window = time.Hour`. Keys are normalised email addresses, and only trusted
addresses ever reach it, so the map is bounded by the size of the trusted list.
`TestLimiterSlidingWindow` covers the window edge and per-key isolation.

## Form size limit

Both POST handlers wrap the body in `http.MaxBytesReader(w, r.Body, 4096)`
before `ParseForm`. An oversized body makes `ParseForm` fail and the handler
returns 400 (`TestRequestBodyTooLarge`). `GET /verify` reads only the query
string, which is bounded by the server's default header limit.

## Server timeouts

`main.go:main` sets on `http.Server`:

| Field | Value |
|-------|-------|
| `ReadHeaderTimeout` | 5 s |
| `ReadTimeout` | 10 s |
| `WriteTimeout` | 15 s |
| `IdleTimeout` | 60 s |

Shutdown on SIGINT/SIGTERM uses `srv.Shutdown` with a 10 s context. Mail
goroutines are not awaited, so a link being sent at that instant can be lost.

The mail client has its own limits in `sendMagicLink`: 10 s to connect and a
30 s deadline on the whole SMTP conversation, so a stuck server cannot pile up
goroutines indefinitely.

## CSP and other headers

`handlers.go:securityHeaders` wraps the entire mux (`main.go:routes`):

- `Content-Security-Policy: default-src 'none'; style-src 'self'; form-action 'self'`:
  no scripts, images, frames or connections at all; styles only from the
  portal's own origin; forms may only post back to it.
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`: the confirm page's URL contains the token, so
  no Referer may carry it anywhere.
- `Cache-Control: no-store`: pages with tokens and addresses are never cached.
  Only `GET /static/` overrides it, with a one-year immutable cache.

All output goes through `html/template`, which escapes `{{.IP}}`, `{{.Token}}`
and error text contextually. Pages have no inline styles or scripts, so the
CSP needs no `unsafe-inline`.

## STARTTLS failing closed

`mail.go:sendMagicLink` calls `c.StartTLS(&tls.Config{ServerName: cfg.Host})`
and returns on any error, before `Auth`. If the server does not offer STARTTLS
or its certificate does not verify for `cfg.Host`, nothing is sent and the
password is never transmitted. `smtp.PlainAuth` independently refuses to send
credentials over an unencrypted connection to a non-localhost server. Implicit
TLS (port 465) is not implemented.

The envelope sender is the address parsed from `SMTP.From`; the recipient is
the normalised trusted address, never raw request text.

## Config validation naming the field

`config.go:applyEnv` prefixes parse errors with the env var name
(`TYC_SMTP_PORT: strconv.Atoi: ...`). `config.go:validate` reports each
problem as `TYC_X (json_key): message` and joins them with `errors.Join`, and
`main` exits with `config: ...` before listening. Validation happens once, at
startup. See [Configuration internals](configuration-internals.md).

## No secrets logged

Every log call site, and what it prints:

| Call site | Output |
|-----------|--------|
| `main` | `config: <validation errors>` (field names, plus the offending text for parse errors; the password is never parsed, so never printed) |
| `main` | `listening on <addr>` |
| `main` | `shutdown: <err>` |
| `render` | `render <page>: <template error>` |
| `handleRequest` | `issue token: <err>` |
| `handleRequest` goroutine | `send mail to <email>: <smtp error>` |
| `handleVerifyPost` | `ipset add <ip>: <runIpset error with argv and ipset output>` |
| `handleVerifyPost` | `whitelisted <ip> for <email>` |

Tokens, passwords and the `Config` struct are never logged. `Config` has no
`String` method; do not add one that prints fields, and do not `%v` the struct
in production code. Unknown email addresses are never logged.
