# Security design

[Developer guide](README.md) > Security design

Each control, where it lives, and what to preserve when changing it. The
threat model for operators is in [the security model](../user/security-model.md).

## Summary

| Control | Location |
|---------|----------|
| Trusted-proxy `X-Forwarded-For` handling | `handlers.go:clientIP`, `config.go:validate` |
| Public IPv4 check | `allow.go:publicIPv4`, called from `clientIP` and `allowlist.add` |
| Allowlist state file, atomic 0600 write | `allow.go:save`, `allow.go:newAllowlist` |
| One address per email | `allow.go:add` |
| Gate semantics (`/check`) | `handlers.go:handleCheck` |
| No shell, no exec | the absence of `os/exec` anywhere in the module |
| Token generation, hashing, single use, expiry | `tokens.go:issue`, `peek`, `consume`, `purge` |
| Anti-enumeration | `handlers.go:handleRequest` |
| Rate limiting (per email and IP, plus per-email cap) | `tokens.go:limiter.allow`, `handlers.go:newServer`, `handleRequest` |
| Form size limit | `handlers.go:handleRequest`, `handleVerifyPost` |
| Server timeouts | `main.go:main` |
| CSP, HSTS and other headers | `handlers.go:securityHeaders`, `main.go:routes` |
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
   peer is inside one of the prefixes in `cfg.proxies` (`p.Contains(peer)`): take the **last** header value, split on commas,
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

`cfg.proxies` is a `[]netip.Prefix` built in `config.go:validate` from
`TrustedProxies`: trimmed, empty entries skipped. An entry that parses with
`netip.ParseAddr` is `Unmap()`ed and becomes a single-address prefix (`/32` or
`/128`); otherwise it must parse with `netip.ParsePrefix` and is stored
`Masked()`, so `172.28.0.5/24` means `172.28.0.0/24`. The default is
`127.0.0.1` and `::1`; the shipped compose file uses the compose network,
`172.28.0.0/24`.

Every address in a trusted prefix can claim any client address, so the range
must contain only the reverse proxy. On the compose network that means only
containers in this project. `TestCheckCIDRTrustedProxy` checks that a peer in
the prefix is trusted and one just outside it is not.

## Public IPv4 check

`allow.go:publicIPv4` returns true only if the address is `Is4()`,
`IsGlobalUnicast()`, outside `240.0.0.0/4` (reserved space, which
`IsGlobalUnicast` accepts apart from `255.255.255.255`), and not `IsPrivate`,
`IsLoopback`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`, `IsMulticast` or
`IsUnspecified`. `Is4()` is false for IPv4-mapped IPv6, which
is why callers `Unmap()` first. The zero `netip.Addr` fails `Is4()`.

It is applied twice: in `clientIP` (so handlers reject early, before touching a
token or the allowlist) and again in `allowlist.add` (so no future caller can
store a bad address), and a third time on load, where non-public keys in the
file are dropped. `TestAllowlistRefusesNonPublicIPv4` covers RFC 1918,
loopback, link-local, multicast, `0.0.0.0`, `240.0.0.1`, broadcast, IPv6,
mapped IPv6 and the zero value, and checks no state file is written.

Note that `IsPrivate` does not cover carrier-grade NAT space (`100.64.0.0/10`)
or documentation ranges; those are treated as public. In practice the portal
only ever sees the ISP's public egress address.

## Allowlist state file

`allow.go` keeps `map[netip.Addr]allowEntry` (address to `{email, expiry}`)
behind one mutex and mirrors it to `TYC_STATE_FILE` (default
`/data/allowlist.json`).

- **Format**: `json.Marshal` of the map, so keys are the canonical
  `netip.Addr` text form and values `{"email": ..., "expires": ...}` with the
  time in RFC 3339, UTC, truncated to whole seconds:
  `{"203.0.113.9":{"email":"alice@example.com","expires":"2026-09-27T01:00:00Z"}}`.
  Request text never reaches the file; only parsed, `publicIPv4`-checked
  addresses and normalised trusted emails do (`TestAllowlistFileFormat`).
- **Atomic, durable write**: `save` creates a temp file in the same directory
  with `os.CreateTemp(dir, ".allowlist-*")`, writes and `Sync`s it, sets mode
  `0600`, `os.Rename`s it over `path`, then `Sync`s the directory. A crash
  leaves either the old file or the new one, never a torn one, and a
  successful unlock survives power loss. Any error removes the temp file.
  Mode `0600` keeps the addresses and emails private to UID 65532.
  `TestAllowlistFileModeAndNoTempLeft` checks the mode and that nothing is left
  behind.
- **Memory follows disk**: `add` builds the new map as a clone, saves it, and
  only then swaps it in, so a failed save changes nothing
  (`TestAllowlistSaveFailureLeavesMemoryUnchanged`). `handleVerifyPost`
  consumes the token only after the save succeeds, so a failed save does not
  burn the link (`TestVerifyPostSaveFailureKeepsToken`).
- **Writes happen only on unlock**, under the lock, after `purge`. Reads
  (`allowed`) never touch the disk.
- **Load**: `newAllowlist` treats a missing file as empty. A file that does not
  parse (bad JSON, a key that is not an address, a value that is not an
  `{email, expires}` object with an RFC 3339 time) is renamed to
  `path + ".corrupt"` with a log line and the list starts empty, so a damaged
  file fails closed (everyone locked) without taking the portal down
  (`TestAllowlistCorruptFileMovedAside`). Only a read error or a failed rename
  is fatal. Non-public addresses and expired entries are dropped on load
  (`TestAllowlistLoadDropsExpiredAndNonPublic`).
- **Expiry and refresh**: `add` sets `now + ttl` whether or not the address is
  already present, matching v0.1's `-exist` refresh.
- **One address per email**: `add` drops any other address held by the same
  email and returns it, so one compromised or careless inbox can unlock only
  one network at a time. An address is a single key, so if two emails unlock
  the same address the latest wins (`TestAllowlistOneAddressPerEmail`,
  `TestAllowlistSameAddressLatestEmailWins`).

## `/check` semantics

`handlers.go:handleCheck` is the only thing standing between the internet and
Jellyfin, so it fails closed:

| Input | Response |
|-------|----------|
| `clientIP` not ok (IPv6, private, loopback, unparsable `X-Forwarded-For`, bad `RemoteAddr`) | 403, `locked` page with `badIPMessage` |
| Public IPv4, not in the allowlist or expired | 403, `locked` page with "This network is not unlocked..." and a link to `cfg.PublicURL` |
| Public IPv4, unlocked | 200, `text/plain`, `ok` |

Caddy's `forward_auth` treats any 2xx as allow and anything else as deny, and
returns the deny response to the client as is. So a template error (500), a
crashed handler or an unreachable app all deny. The endpoint reveals one bit about one
address: the caller's own, as seen through the trusted proxy. It does not log,
does not touch tokens or the limiter, and costs a mutex and a map lookup.

`/check` is also reachable on the portal hostname, since Caddy proxies all of
`hello.example.com` to the app. That exposes nothing more: a visitor learns
whether their own network is unlocked.

## No shell, no exec

v0.1 ran `ipset` through `os/exec` with a fixed argv. That code is gone.
Nothing in the module imports `os/exec`, and the app never starts a process.
The runtime image adds nothing to the Alpine base (whose BusyBox the app
never calls). Keep it that way: the container runs with no capabilities
precisely because it only serves HTTP, sends SMTP and writes one file.

## Tokens

`tokens.go`:

| Property | Implementation |
|----------|----------------|
| Generation | `issue`: 32 bytes from `crypto/rand.Read`, encoded with `base64.RawURLEncoding` (43 URL-safe characters, no padding). |
| Storage | Only `sha256.Sum256(token)` is stored, as a `[32]byte` map key, with `{email, expiry}`. A memory dump or debug print does not reveal usable links (`TestTokenStoresOnlyHash`). |
| Lookup | Hash the presented token, then a map lookup. The lookup is on the hash, so there is no byte-by-byte comparison of secrets to time. |
| Single use | `consume` deletes the entry under the lock. `POST /verify` peeks, saves the unlock, and only then consumes, so a failed save leaves the link usable. The cost: two concurrent POSTs with the same token can both pass `peek` and both unlock. Each can only unlock the address it arrives from, for the same email, and the second replaces the first under one-address-per-email, so this cannot unlock more than one network. `peek` (used by `GET /verify`) never deletes. |
| Expiry | `expiry = now + TokenTTL`. `purge` runs at the start of `issue`, `peek` and `consume` and deletes every entry with `!now.Before(expiry)`, so an expired token is never returned and does not linger. |
| Scope | A token records the email but not the client address; the address is taken from the POST. |

Tokens are in memory only and vanish on restart.

## Anti-enumeration

`handlers.go:handleRequest` renders the identical `sent` page with status 200
for:

- a trusted address that gets a link,
- a trusted address that is rate limited,
- an unknown address,
- any address from a client that is not public IPv4,
- a trusted address where token generation failed.

For unknown addresses and bad client addresses it does no work:
`ok && s.trusted[email] && s.limiter.allow(email+"|"+ip) && s.capper.allow(email)`
short-circuits, so no limiter entry and no token are created
(`TestRequestSameResponseForKnownAndUnknown`, `TestRequestFromBadIPDoesNoWork`). Sending runs
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

`newServer` builds two, both with a fixed `window = time.Hour`:

| Limiter | Key | Limit | Purpose |
|---------|-----|-------|---------|
| `limiter` | `email + "\|" + clientIP` | `RequestsPerEmailPerHour` (3) | Normal use. A stranger on another network cannot use up the owner's quota at home. |
| `capper` | `email` | `4 * RequestsPerEmailPerHour` (12) | Caps mail to one inbox across all networks, so the portal cannot be used to spam it from many addresses. |

`capper` is consulted only after `limiter` allows, so requests refused per IP
do not eat into the cap. Only trusted emails from public IPv4 clients reach
either, so `capper` is bounded by the trusted list and `limiter` by trusted
emails times the client addresses seen in the last hour.
`TestLimiterSlidingWindow` covers the window edge and per-key isolation;
`TestRequestLimitPerEmailAndIP` and `TestRequestPerEmailCapAcrossIPs` cover
the two keys.

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

- `Content-Security-Policy: default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`:
  no scripts, images, frames or connections at all; styles only from the
  portal's own origin; forms may only post back to it; no other site may frame
  the pages (clickjacking of the Unlock button); no `<base>` rewriting.
- `Strict-Transport-Security: max-age=31536000`: browsers stay on HTTPS for a
  year. Sent on every response, including the locked page on the Jellyfin
  hostname.
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
| `main` | `allowlist: <error reading the state file or moving a corrupt one aside>` |
| `newAllowlist` | `allowlist <path>: <parse error>; moving it to <path>.corrupt and starting empty` |
| `handleVerifyPost` | `allowlist add <ip>: <file system error>` |
| `handleVerifyPost` | `unlocked <ip> for <email>`, with `, replacing <old>` when the email's previous address was dropped |

Tokens, passwords and the `Config` struct are never logged. `Config` has no
`String` method; do not add one that prints fields, and do not `%v` the struct
in production code. Unknown email addresses are never logged.
