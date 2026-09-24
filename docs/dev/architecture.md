# Architecture

[Developer guide](README.md) > Architecture

## Package layout

Everything is in one `package main` at the repository root. There are no
sub-packages and no module dependencies.

```
.
├── main.go           entry point, routes, embedded static FS, http.Server
├── config.go         Config types, loadConfig, applyEnv, validate
├── handlers.go       template loading, server type, middleware, clientIP, four handlers
├── templates/        layout.html and one body file per page, embedded
├── tokens.go         tokenStore and limiter
├── mail.go           composeMessage, sendMagicLink
├── ipset.go          runIpset, ipsetAdd, publicIPv4
├── *_test.go         one test file per source file except main.go
└── static/
    └── pico.classless.min.css   vendored Pico CSS v2.1.1, embedded
```

## Files and functions

### `main.go`

| Name | Purpose |
|------|---------|
| `staticFS` (`embed.FS`) | `//go:embed static`: the vendored Pico stylesheet and `site.css`, a few rules for the index header. |
| `(*server).routes() http.Handler` | Builds the `http.ServeMux` with the four page routes and `GET /static/`, and wraps it in `securityHeaders`. |
| `main()` | `loadConfig`, fatal on error; `newServer` with a sender that calls `sendMagicLink(cfg.SMTP, ...)`; starts `http.Server` with timeouts; shuts down gracefully (10 s) on SIGINT or SIGTERM. |

### `config.go`

| Name | Purpose |
|------|---------|
| `Duration` | `time.Duration` that unmarshals from a JSON string such as `"72h"`. |
| `(*Duration).UnmarshalJSON` | Decodes a JSON string and parses it with `time.ParseDuration`. |
| `SMTPConfig` | Host, port, username, password, from. |
| `Config` | All settings, plus the unexported `proxies []netip.Addr` filled by `validate`. |
| `loadConfig() (*Config, error)` | Defaults, then JSON file from `TYC_CONFIG`, then `applyEnv`, then `validate`. |
| `applyEnv(*Config) error` | One `os.LookupEnv` per `TYC_*` key, with string, list, int and duration helpers. |
| `(*Config).validate() error` | Normalises emails and proxies, checks every field, returns all problems joined. |

Details: [Configuration internals](configuration-internals.md).

### `handlers.go`

| Name | Purpose |
|------|---------|
| `templateFS` | `embed.FS` holding `templates/`: `layout.html` plus one file per page. |
| `pages` | Map of five parsed `html/template` sets, one per page: `index`, `sent`, `confirm`, `success`, `error`. Each set is `layout.html` parsed together with that page's `{{define "body"}}` file via `template.ParseFS`, so executing it renders the full layout. Built once at init with `template.Must`. |
| `badIPMessage` | Text shown when the client is not a public IPv4 address. |
| `server` | Holds `cfg`, `tokens *tokenStore`, `limiter *limiter`, `trusted map[string]bool`, `send func(to, link string) error`. |
| `newServer(cfg, send) *server` | Builds the trusted-email set, a token store with `TokenTTL`, and a limiter of `RequestsPerEmailPerHour` per hour. |
| `securityHeaders(next) http.Handler` | Middleware setting CSP, `nosniff`, `no-referrer`, `no-store` on every response. |
| `render(w, status, page, data)` | Executes a template into a buffer, then writes `Content-Type`, status and body. On template error: logs and returns 500. |
| `(*server).clientIP(r) (netip.Addr, bool)` | Client address from `RemoteAddr` or, from a trusted proxy, the last `X-Forwarded-For` hop; `ok` only for public IPv4. |
| `(*server).handleIndex` | `GET /`: the email form. |
| `(*server).handleRequest` | `POST /request`: maybe issue a token and send mail; always the same page. |
| `(*server).handleVerifyGet` | `GET /verify`: check address and token without consuming; show confirm form. |
| `(*server).handleVerifyPost` | `POST /verify`: check address, consume token, `ipsetAdd`, show success. |

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
| `composeMessage(from, to, link, date) []byte` | Plain-text RFC 5322 message with CRLF line endings, subject "Your Jellyfin access link". |
| `sendMagicLink(cfg, to, link) error` | Dial (10 s), 30 s deadline, `STARTTLS` with `ServerName: cfg.Host`, `PLAIN` auth, `MAIL`/`RCPT`/`DATA`, `QUIT`. |

### `ipset.go`

| Name | Purpose |
|------|---------|
| `runIpset` (package variable) | `exec.Command("ipset", args...).CombinedOutput()`; wraps errors with argv and output. Replaced in tests. |
| `ipsetAdd(set, ip, ttl) error` | Refuses non-public-IPv4, then `runIpset("add", set, ip.String(), "timeout", <seconds>, "-exist")`. |
| `publicIPv4(ip) bool` | `Is4()` and not private, loopback, link-local unicast or multicast, multicast, or unspecified. |

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
| `GET /static/` | `http.FileServerFS(staticFS)` with a long cache header |

### `GET /`

1. `securityHeaders` sets the headers.
2. `handleIndex` renders `index` with status 200: an email field posting to
   `/request`.

### `POST /request`

1. Body limited to 4096 bytes with `http.MaxBytesReader`; `ParseForm`. A parse
   error (including an oversized body) renders `error` with 400.
2. `email` is lower-cased and trimmed.
3. If `s.trusted[email]` **and** `s.limiter.allow(email)`: `s.tokens.issue`.
   The limiter is only consulted for trusted addresses (short-circuit `&&`).
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
3. `s.tokens.consume(token)`. Missing: 400 "invalid or has expired".
4. `ipsetAdd(cfg.IpsetName, ip, WhitelistTTL)`. Error: logged as
   `ipset add <ip>: <err>`, 500 with a generic message. The token is already
   consumed at this point.
5. Logs `whitelisted <ip> for <email>` and renders `success` with 200, showing
   the address and `int(ttl.Hours())`.

The address used is the one seen on the POST, not the one shown on the GET.

### Sequence

```
Browser             Caddy            server (handlers.go)       tokenStore   limiter   mail goroutine   ipset
   |                  |                     |                        |          |            |            |
   |-- POST /request ->|-- XFF: client ---->|                        |          |            |            |
   |                  |                     |-- trusted[email]? ---- |          |            |            |
   |                  |                     |-- allow(email) -------------------->|          |            |
   |                  |                     |-- issue(email) ------->|          |            |            |
   |                  |                     |<-- token --------------|          |            |            |
   |                  |                     |-- go send(email, link) ------------------------>|           |
   |<-- 200 "sent" ---|<-------------------|                        |          |            |-- SMTP --> |
   |                  |                     |                        |          |            | (STARTTLS) |
   |  (user opens link from email)          |                        |          |            |            |
   |-- GET /verify?token ->|--------------->|                        |          |            |            |
   |                  |                     |-- clientIP(r)          |          |            |            |
   |                  |                     |-- peek(token) -------->|          |            |            |
   |<-- 200 "confirm" (IP, hidden token) ---|                        |          |            |            |
   |                  |                     |                        |          |            |            |
   |-- POST /verify (token) --------------->|                        |          |            |            |
   |                  |                     |-- clientIP(r)          |          |            |            |
   |                  |                     |-- consume(token) ----->|          |            |            |
   |                  |                     |<-- email --------------|          |            |            |
   |                  |                     |-- ipsetAdd(set, ip, ttl) ------------------------------------>|
   |                  |                     |     runIpset("add", set, ip, "timeout", secs, "-exist")      |
   |                  |                     |<------------------------------------------------------ ok ---|
   |<-- 200 "success" ----------------------|                        |          |            |            |
```

## Security-header middleware

`securityHeaders` wraps the whole mux, so it runs for every response,
including 404s, 405s and the stylesheet:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | `default-src 'none'; style-src 'self'; form-action 'self'` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |
| `Cache-Control` | `no-store` |

The static handler overwrites `Cache-Control` afterwards (see below). `render`
adds `Content-Type: text/html; charset=utf-8`. Pages contain no scripts, no
inline styles and no external resources, which is what lets the CSP be this
strict.

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
