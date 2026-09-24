# Architecture

[Developer guide](README.md) > Architecture

## Package layout

Everything is in one `package main` at the repository root. There are no
sub-packages and no module dependencies.

```
.
├── main.go           entry point, routes, embedded static FS, http.Server
├── config.go         Config types, loadConfig, applyEnv, validate
├── handlers.go       template loading, server type, middleware, clientIP, five handlers
├── templates/        layout.html and one body file per page, embedded
├── tokens.go         tokenStore and limiter
├── mail.go           composeMessage, sendMagicLink
├── allow.go          allowlist (in memory plus JSON state file), publicIPv4
├── *_test.go         one test file per source file except main.go
└── static/
    └── pico.classless.min.css   vendored Pico CSS v2.1.1, embedded
```

## Files and functions

### `main.go`

| Name | Purpose |
|------|---------|
| `staticFS` (`embed.FS`) | `//go:embed static`: the vendored Pico stylesheet and `site.css`, a few rules for the index header. |
| `(*server).routes() http.Handler` | Builds the `http.ServeMux` with the four page routes, `GET /check` and `GET /static/`, and wraps it in `securityHeaders`. |
| `main()` | `loadConfig`, fatal `config: ...` on error; `newAllowlist(cfg.StateFile, WhitelistTTL)`, fatal `allowlist: ...` on error (unreadable file, or a corrupt one that cannot be moved aside); `newServer(cfg, allow, send)` with a sender that calls `sendMagicLink(cfg.SMTP, ...)`; starts `http.Server` with timeouts; shuts down gracefully (10 s) on SIGINT or SIGTERM. |

### `config.go`

| Name | Purpose |
|------|---------|
| `Duration` | `time.Duration` that unmarshals from a JSON string such as `"72h"`. |
| `(*Duration).UnmarshalJSON` | Decodes a JSON string and parses it with `time.ParseDuration`. |
| `SMTPConfig` | Host, port, username, password, from. |
| `Config` | All settings, plus the unexported `proxies []netip.Prefix` filled by `validate`. |
| `loadConfig() (*Config, error)` | Refuses `TYC_IPSET_NAME`; then defaults, JSON file from `TYC_CONFIG`, `applyEnv`, `validate`. |
| `applyEnv(*Config) error` | One `os.LookupEnv` per `TYC_*` key, with string, list, int and duration helpers. |
| `(*Config).validate() error` | Normalises emails and proxies, checks every field, returns all problems joined. |

Details: [Configuration internals](configuration-internals.md).

### `handlers.go`

| Name | Purpose |
|------|---------|
| `templateFS` | `embed.FS` holding `templates/`: `layout.html` plus one file per page. |
| `pages` | Map of six parsed `html/template` sets, one per page: `index`, `sent`, `confirm`, `success`, `error`, `locked`. Each set is `layout.html` parsed together with that page's `{{define "body"}}` file via `template.ParseFS`, so executing it renders the full layout. Built once at init with `template.Must`. |
| `badIPMessage` | Text shown when the client is not a public IPv4 address. |
| `server` | Holds `cfg`, `allow *allowlist`, `tokens *tokenStore`, `limiter *limiter` (per email and client IP), `capper *limiter` (per email across all IPs), `trusted map[string]bool`, `send func(to, link string) error`. |
| `newServer(cfg, allow, send) *server` | Builds the trusted-email set, a token store with `TokenTTL`, a limiter of `RequestsPerEmailPerHour` per hour and a capper of `4 * RequestsPerEmailPerHour` per hour; keeps the allowlist it is given; sets `static` (`PublicURL` without trailing slash) and `csp` (with the portal origin in `style-src`). |
| `(*server).securityHeaders(next) http.Handler` | Middleware setting CSP (`s.csp`), HSTS, `nosniff`, `no-referrer`, `no-store` on every response. |
| `(*server).render(w, status, page, data)` | Executes a template with `{Static: s.static, Data: data}` into a buffer, then writes `Content-Type`, status and body. On template error: logs and returns 500. |
| `(*server).clientIP(r) (netip.Addr, bool)` | Client address from `RemoteAddr` or, when `RemoteAddr` is inside a trusted proxy prefix, the last `X-Forwarded-For` hop; `ok` only for public IPv4. |
| `(*server).handleIndex` | `GET /`: the email form. |
| `(*server).handleRequest` | `POST /request`: maybe issue a token and send mail; always the same page. |
| `(*server).handleVerifyGet` | `GET /verify`: check address and token without consuming; show confirm form. |
| `(*server).handleVerifyPost` | `POST /verify`: check address, peek token, `s.allow.add`, then consume the token, show success. |
| `(*server).handleCheck` | `GET /check`: Caddy `forward_auth` target. `200 ok` if the caller is unlocked, else `403` with the `locked` page. |

### `tokens.go`

| Name | Purpose |
|------|---------|
| `tokenEntry` | `email` and `expiry`. |
| `tokenStore` | Mutex-guarded `map[[32]byte]tokenEntry` keyed by `sha256(token)`, with an injectable `now`. |
| `newTokenStore(ttl)` | Empty store using `time.Now`. |
| `(*tokenStore).issue(email) (string, error)` | 32 random bytes, base64url without padding; stores the hash with expiry `now + ttl`. |
| `(*tokenStore).peek(tok) (string, bool)` | Looks up without removing. |
| `(*tokenStore).consume(tok) (string, bool)` | Looks up and deletes. |
| `(*tokenStore).purge()` | Drops expired entries. Called under the lock by all three methods above. |
| `limiter` | Mutex-guarded `map[string][]time.Time` of recent events per key. |
| `newLimiter(limit, window)` | Empty limiter using `time.Now`. |
| `(*limiter).allow(key) bool` | Sliding-window check; records the event if allowed. |

### `mail.go`

| Name | Purpose |
|------|---------|
| `messageID(fromAddr) string` | `<32 random hex chars@domain of fromAddr>`, for the `Message-ID` header. |
| `composeMessage(from, to, link, msgID, date) []byte` | Plain-text RFC 5322 message with CRLF line endings, subject "Your Jellyfin access link", and a `Message-ID`. |
| `sendMagicLink(cfg, to, link) error` | Dial (10 s), 30 s deadline, `STARTTLS` with `ServerName: cfg.Host`, `PLAIN` auth, `MAIL`/`RCPT`/`DATA`, `QUIT`. |

### `allow.go`

| Name | Purpose |
|------|---------|
| `allowEntry` | `Email` (JSON `email`) and `Expires` (JSON `expires`). |
| `allowlist` | `mu sync.Mutex`, `path` (the state file), `ttl` (`WhitelistTTL`), injectable `now`, and `m map[netip.Addr]allowEntry`. |
| `newAllowlist(path, ttl) (*allowlist, error)` | Loads `path` if it exists; a missing file is an empty list. A JSON error (including a bad address key, a non-RFC 3339 time, or the old address-to-string shape) logs `allowlist <path>: <err>; moving it to <path>.corrupt and starting empty`, renames the file aside and returns an empty list. Only a read error or a failed rename is returned, and `main` exits. Non-public addresses and expired entries are dropped on load. |
| `(*allowlist).add(ip, email) (replaced netip.Addr, err error)` | Refuses anything `publicIPv4` rejects. Under the lock: `purge`; clone the map; delete any other address held by `email` (returned as `replaced`); set `ip` to `{email, now + ttl}` (UTC, whole seconds; re-adding refreshes it, and the latest email wins for a shared address); `save` the clone; only then swap it in. A failed save leaves memory unchanged. |
| `(*allowlist).allowed(ip) bool` | Under the lock: `purge`, then a map lookup. |
| `(*allowlist).purge()` | Deletes entries with `!now.Before(Expires)`. Caller holds `mu`. |
| `(*allowlist).save(m) error` | `json.Marshal(m)`; `os.CreateTemp(dir, ".allowlist-*")`; write, `Sync`, close; `Chmod 0600`; `os.Rename` over `path`; `Sync` the directory. The temp file is removed on any error. Caller holds `mu`. |
| `publicIPv4(ip) bool` | `Is4()`, `IsGlobalUnicast()`, not in `240.0.0.0/4` (reserved, including broadcast), and not private, loopback, link-local unicast or multicast, multicast, or unspecified. |

The state file is a JSON object of IPv4 string to entry,
`{"203.0.113.9":{"email":"alice@example.com","expires":"2026-09-27T01:00:00Z"}}`,
default `/data/allowlist.json`.

## Routes

Registered in `routes()` with Go 1.22+ method patterns. Anything else is 404,
and a wrong method on a known path is 405 (for example `POST /`). `GET`
patterns also match `HEAD`.

| Pattern | Handler |
|---------|---------|
| `GET /{$}` | `handleIndex` (exactly `/`, not a prefix) |
| `POST /request` | `handleRequest` |
| `GET /verify` | `handleVerifyGet` |
| `POST /verify` | `handleVerifyPost` |
| `GET /check` | `handleCheck` |
| `GET /static/` | `http.FileServerFS(staticFS)` with a long cache header |

### `GET /`

1. `securityHeaders` sets the headers.
2. `handleIndex` renders `index` with status 200: an email field posting to
   `/request`.

### `POST /request`

1. Body limited to 4096 bytes with `http.MaxBytesReader`; `ParseForm`. A parse
   error (including an oversized body) renders `error` with 400.
2. `email` is lower-cased and trimmed; `clientIP` is computed.
3. If the client is public IPv4 **and** `s.trusted[email]` **and**
   `s.limiter.allow(email + "|" + ip)` **and** `s.capper.allow(email)`:
   `s.tokens.issue`. The chain short-circuits, so a bad client address or an
   unknown email touches neither limiter, and a request over the per-IP limit
   does not count against the per-email cap.
4. On a token, the link is `strings.TrimRight(PublicURL, "/") + "/verify?token=" + tok`,
   and `s.send(email, link)` runs in a new goroutine. A send error is logged as
   `send mail to <email>: <err>`.
5. In every case, `sent` is rendered with 200.

### `GET /verify?token=...`

1. `clientIP`. Not public IPv4: `error` with `badIPMessage`, 400.
2. `s.tokens.peek(token)`. Missing or expired: `error` "That link is invalid
   or has expired.", 400.
3. `confirm` with 200, showing the address and a POST form carrying the token
   in a hidden field. The token is not consumed.

### `POST /verify`

1. Body limited to 4096 bytes; `ParseForm`; error: 400.
2. `clientIP`. Not public IPv4: 400 with `badIPMessage`. The token is **not**
   consumed, so the user can retry after fixing their connection.
3. `s.tokens.peek(token)`. Missing: 400 "invalid or has expired".
4. `s.allow.add(ip, email)`. Error (in practice, the state file cannot be
   written): logged as `allowlist add <ip>: <err>`, 500 with a generic
   message. The token is **not** consumed, so the user can press Unlock again
   once the problem is fixed.
5. `s.tokens.consume(token)`.
6. Logs `unlocked <ip> for <email>`, or `unlocked <ip> for <email>, replacing
   <old>` when the email's previous address was dropped, and renders `success`
   with 200, showing the address and `int(WhitelistTTL.Hours())`.

The address used is the one seen on the POST, not the one shown on the GET.

### `GET /check`

Called by Caddy's `forward_auth` before every request to the Jellyfin
hostname. Caddy sends a `GET` to `uri /check` with `X-Forwarded-For` set to
the client address; on a 2xx it proxies the original request to Jellyfin, on
anything else it returns this response to the client unchanged.

1. `clientIP`, exactly as for the other routes. Not public IPv4 (IPv6,
   private, unparsable `X-Forwarded-For`): 403, `locked` page with
   `badIPMessage`.
2. `s.allow.allowed(ip)`. True: 200, `Content-Type: text/plain`, body `ok`.
3. Otherwise: 403, `locked` page with "This network is not unlocked. Open the
   portal from a device on this network to unlock it." and a link to
   `cfg.PublicURL`.

Nothing is logged. The handler does a mutex and a map lookup, so running on
every Jellyfin request (including media segments) is cheap. The response says
only whether the caller's own address is allowed. It goes through
`securityHeaders` like every route, so the 403 page carries the same CSP.

### Sequence

```
Browser/TV            Caddy                 server             tokenStore   mail         allowlist   Jellyfin
   |                    |                      |                    |          |              |          |
   |-- POST /request -->|-- XFF: client ------>|                    |          |              |          |
   |                    |                      |-- issue(email) --->|          |              |          |
   |                    |                      |-- go send -------------------->|             |          |
   |<-- 200 "sent" -----|<---------------------|                    |          |-- SMTP -->   |          |
   |                    |                      |                    |          |              |          |
   |-- GET /verify ---->|--------------------->|-- peek(token) ---->|          |              |          |
   |<-- 200 "confirm" --|<---------------------|                    |          |              |          |
   |                    |                      |                    |          |              |          |
   |-- POST /verify --->|--------------------->|-- peek(token) ---->|          |              |          |
   |                    |                      |-- add(ip, email) --------------------------->|          |
   |                    |                      |                    |          | temp, rename |          |
   |                    |                      |-- consume(token) ->|          |              |          |
   |<-- 200 "success" --|<---------------------|                    |          |              |          |
   |                    |                      |                    |          |              |          |
   |-- GET jellyfin --->|                      |                    |          |              |          |
   |                    |-- GET /check ------->|                    |          |              |          |
   |                    |   XFF: client        |-- allowed(ip) ------------------------------>|          |
   |                    |<-- 200 ok / 403 -----|                    |          |              |          |
   |                    |-- on 200: reverse_proxy over WireGuard -------------------------------------->|
   |<-- Jellyfin or 403 |                      |                    |          |              |          |
```

The first three exchanges are on `hello.example.com`; the last is on
`jellyfin.example.com`.

## Security-header middleware

`securityHeaders` wraps the whole mux, so it runs for every response,
including 404s, 405s and the stylesheet:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | `default-src 'none'; style-src 'self' <portal origin>; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`, where the portal origin is the scheme and host of `PublicURL` (`https://hello.example.com`) |
| `Strict-Transport-Security` | `max-age=31536000` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |
| `Cache-Control` | `no-store` |

The static handler overwrites `Cache-Control` afterwards (see below). `render`
adds `Content-Type: text/html; charset=utf-8`. Pages contain no scripts, no
inline styles and no resources outside the portal, which is what lets the CSP
be this strict.

`layout.html` links the stylesheets by absolute URL,
`{{.Static}}/static/pico.classless.min.css` and `{{.Static}}/static/site.css`,
where `Static` is the portal URL. On the portal that is the same origin. On the
gated Jellyfin hostname, where Caddy returns the `locked` page from `/check`,
a relative `/static/...` link would itself go through `forward_auth` and be
refused; the absolute link loads the CSS from the ungated portal instead, and
the portal origin in `style-src` allows it. Page bodies read their own values
from `.Data`.

## Embedded static file

`static/pico.classless.min.css` (Pico CSS v2.1.1, MIT, classless build) is
compiled in with `//go:embed` and served by `http.FileServerFS(staticFS)` at
`/static/pico.classless.min.css`, with `Cache-Control: public,
max-age=31536000, immutable`. Because the embedded FS contains the `static/`
directory, the request path maps directly onto it. `GET /static/` itself
returns a directory listing of that one file.

The file is never fetched at build or run time. To upgrade it, copy the new
file from
`https://cdn.jsdelivr.net/npm/@picocss/pico@<version>/css/pico.classless.min.css`,
check its SHA-256, and update the "Vendored assets" section of the README.
Because the URL does not change, returning clients may keep the old copy
cached for up to a year; that is acceptable for a stylesheet.
