# takeyourcoat: plan

A tiny Go captive portal that lets a trusted person unlock their
household's public IPv4 address for a few days, so every device on that
network can reach Jellyfin. Caddy enforces it by asking the app on every
Jellyfin request (`forward_auth`). Stdlib only. One binary, one JSON state
file, one unprivileged container next to Caddy.

## Decisions (settled)

| Topic         | Decision |
|---------------|----------|
| Placement     | Runs on the public VPS in one compose project with Caddy. Jellyfin lives at home behind a WireGuard tunnel to that VPS. |
| Enforcement   | In the app, via Caddy `forward_auth`: Caddy sends `GET /check` for every Jellyfin request; 200 proxies, 403 returns the locked page. Replaces ipset/iptables, which cannot see hostnames. |
| Container     | Bridge network shared with Caddy, no published port. UID 65532, `cap_drop: ALL`, no capabilities added, read-only rootfs, `no-new-privileges`, one named volume at `/data`. |
| Ingress       | Caddy (in compose) terminates TLS for both hostnames on 443: `hello.example.com` proxies to `takeyourcoat:8080`; `jellyfin.example.com` is gated by `forward_auth` and proxies to Jellyfin over WireGuard. App trusts `X-Forwarded-For` only from configured proxy addresses or CIDRs. |
| Email         | SMTP with STARTTLS via `net/smtp` + `crypto/tls`. |
| Config        | Environment variables first, optional JSON file second. Every key has a `TYC_` env var. Compose `environment:` block can configure everything with no file mounted. |
| Verification  | Magic link opens a page showing the detected IP with a confirm button. Only the POST whitelists. Defeats link prefetchers. |
| Address family| IPv4 only. IPv6 clients get a clear error, and the locked page on Jellyfin. |
| Defaults      | Whitelist 72h, one unlocked address per email, token 15m single use, 3 link requests per email per client IP per hour (capped at 12 per email), identical response for known and unknown emails. |
| Dependencies  | None outside the Go standard library. Pico CSS v2.1.1 (MIT, classless build) is vendored as a single file in `static/` and embedded in the binary. |

## Port layout (resolved)

Both hostnames share 443. The v0.1 split (Jellyfin on 8920 so a port-based
iptables rule could gate it) is gone, and no second public IP is needed.

## Host prerequisites

Docker, and TCP 80/443 plus UDP 443 open in the cloud firewall and host
firewall. No ipset, no iptables rules, no persistence, no AAAA workaround.

## Request flow

```
GET  /               → email form
POST /request        → if email trusted and under rate limit: mint token, send mail (async)
                       → always render "if that address is trusted, a link is on its way"
GET  /verify?token=  → look up token (not consumed) → show detected IPv4 + confirm button
POST /verify         → peek token → allowlist.add(ip, email) → consume token → success page
GET  /check          → Caddy forward_auth target: allowed(clientIP) ? 200 "ok" : 403 locked page
```

No cookies, no sessions, no database. The token is the only credential.

## Security properties

- **Client IP**: `netip.ParseAddr` on the last `X-Forwarded-For` hop only when `RemoteAddr` is inside a trusted proxy prefix; otherwise `RemoteAddr`. Must be IPv4, must not be private, loopback, link-local or multicast. Anything else is rejected before touching the allowlist.
- **Allowlist file**: `allow.go` keeps `map[netip.Addr]{email, expiry}` under a mutex and writes it as JSON to `TYC_STATE_FILE` with mode 0600, via a synced temp file, `os.Rename` and a directory sync, so a crash never leaves a torn file. Memory changes only after the save; the token is consumed only after it. A corrupt file is moved aside and the list starts empty. Only public IPv4 is ever added, and each email holds one address. No shell or exec anywhere.
- **`/check`**: a mutex and a map lookup. Answers only allowed or not, for the caller's own address.
- **Tokens**: 32 bytes from `crypto/rand`, base64url in the link. Server stores `sha256(token)` → {email, expiry}. Single use, purged on expiry.
- **Enumeration and timing**: same page and status for known and unknown emails. Mail is sent in a goroutine so response time does not reveal whether a send happened.
- **Rate limiting**: sliding windows per email and client IP (default 3/hour) and per email across all IPs (4x, 12/hour), so the portal cannot be used to spam the trusted list. Requests for unknown emails or from non-public client addresses do no work.
- **HTTP hygiene**: `http.Server` with Read/Write/Idle timeouts, `MaxBytesReader` on forms, method checks, `html/template` for all output, `Cache-Control: no-store` on pages, `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, CSP `default-src 'none'; style-src 'self' <portal origin>; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`, HSTS. Stylesheets load from the portal origin by absolute URL, so the locked page is styled on the gated hostname. No inline styles, no scripts, no external requests from the browser. Listens on loopback only by default.
- **Container**: UID 65532, no capabilities, read-only rootfs, no-new-privileges, no host network. Writes only `/data`.
- **Secrets**: SMTP password arrives via env from a 0600 `.env` file, or via the JSON file. It is never logged and the config struct has no `String()` that could leak it. Logs record email and IP on success only.
- **Refresh semantics**: re-verifying sets the expiry to now + TTL again rather than erroring.

## Configuration

Two sources, merged in this order: JSON file (if `TYC_CONFIG` points at
one), then environment variables, which win. Either source alone is
enough. In the normal Docker deployment there is no JSON file at all;
everything lives in `docker-compose.yml` plus a `.env` for the SMTP
password.

| Env var                          | JSON key                       | Default            |
|----------------------------------|--------------------------------|--------------------|
| `TYC_LISTEN`                     | `listen`                       | `127.0.0.1:8080`   |
| `TYC_PUBLIC_URL`                 | `public_url`                   | required           |
| `TYC_TRUSTED_PROXIES`            | `trusted_proxies`              | `127.0.0.1,::1` (addresses or CIDRs) |
| `TYC_TRUSTED_EMAILS`             | `trusted_emails`               | required           |
| `TYC_STATE_FILE`                 | `state_file`                   | `/data/allowlist.json` |
| `TYC_WHITELIST_TTL`              | `whitelist_ttl`                | `72h`              |
| `TYC_TOKEN_TTL`                  | `token_ttl`                    | `15m`              |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR`| `requests_per_email_per_hour`  | `3`                |
| `TYC_SMTP_HOST`                  | `smtp.host`                    | required           |
| `TYC_SMTP_PORT`                  | `smtp.port`                    | `587`              |
| `TYC_SMTP_USERNAME`              | `smtp.username`                | required           |
| `TYC_SMTP_PASSWORD`              | `smtp.password`                | required           |
| `TYC_SMTP_FROM`                  | `smtp.from`                    | required           |

Lists are comma-separated in env vars. Durations are Go duration strings.
Config is validated once at startup and the process refuses to start on
any missing or malformed field, naming the field. Setting the removed
`TYC_IPSET_NAME` at all is a startup error pointing at the README. Emails are compared
case-insensitively after trimming. The password is never logged.

Equivalent JSON, for anyone who prefers a file:

```json
{
  "public_url": "https://hello.example.com",
  "trusted_emails": ["alice@example.com", "bob@example.com"],
  "smtp": {
    "host": "smtp.fastmail.com",
    "username": "you@example.com",
    "password": "app-password",
    "from": "Jellyfin Access <you@example.com>"
  }
}
```

## Code layout

Single `package main`, roughly 350 lines across five files. Function list
is the whole API surface:

| File          | Functions |
|---------------|-----------|
| `main.go`     | `main`: load config, build mux (handlers + embedded static), run server with timeouts and graceful shutdown |
| `config.go`   | `loadConfig` (file then env), `Duration.UnmarshalJSON`, `applyEnv` (one `os.LookupEnv` per key) |
| `handlers.go` | `handleIndex`, `handleRequest`, `handleVerifyGet`, `handleVerifyPost`, `handleCheck`, `clientIP` |
| `tokens.go`   | `tokenStore.issue`, `tokenStore.peek`, `tokenStore.consume`, `limiter.allow`; mutex-guarded maps, expiry checked on access |
| `mail.go`     | `sendMagicLink`: STARTTLS dial, `smtp.PlainAuth`, plain-text body |
| `allow.go`    | `allowlist`: `newAllowlist`, `add`, `allowed`, `purge`, `save`; `publicIPv4`. Replaces `ipset.go` |

HTML lives in `templates/` as embedded `html/template` files: `layout.html`
plus one `{{define "body"}}` file per page, parsed per page with
`template.ParseFS`. Six short pages (including `locked`, served by `/check`) sharing one layout, no JavaScript. Styling comes
from Pico CSS classless, so the markup is plain semantic HTML
(`main`, `article`, `form`, `label`, `input`, `button`) with no classes.

`static/pico.classless.min.css` is embedded with `//go:embed` and served
at `/static/pico.classless.min.css` by `http.FileServerFS` with
`Cache-Control: public, max-age=31536000, immutable`. The file is never
fetched at build or run time; upgrading it is a manual copy and a note
in the README. Source: `https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.classless.min.css`,
SHA-256 `61207a40ffc02a42d1e50143651c121beab70ed413c934c1ff84fa263ba436b0`.

## Docker

- `Dockerfile`: multi-stage. `golang:1.27-alpine` builds with
  `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`. Runtime is `alpine:3.23`
  with nothing added: `/data` owned by 65532, `USER 65532:65532`,
  `VOLUME /data`. Published as `ghcr.io/<you>/takeyourcoat` via a GitHub
  Actions workflow on tag push, so the VPS only pulls.
- `docker-compose.yml` includes Caddy, because `forward_auth` makes it part
  of the design: `caddy` publishes 80, 443 and 443/udp and mounts the
  `Caddyfile`; `takeyourcoat` publishes nothing, listens on `0.0.0.0:8080`
  on the `web` network (`172.28.0.0/24`), trusts exactly Caddy's fixed
  address `172.28.0.10` as proxy (not the /24, which holds the host's gateway), and
  keeps its allowlist on the `tyc-data` volume. No host network, no
  `cap_add`, no `user:` line.
- `Caddyfile` in the repo with placeholders: `hello` site proxies to the
  app; `jellyfin` site runs `forward_auth takeyourcoat:8080 { uri /check }`
  then proxies to the WireGuard peer.
- The SMTP password comes from a `.env` file next to the compose file, mode
  0600, gitignored. Anyone who prefers a JSON file adds
  `TYC_CONFIG=/etc/takeyourcoat/config.json` and a read-only volume.

## Public repository hygiene

The repo and image are public. The design assumes attackers read the
code; what must not leak is the deployment.

- `.gitignore` covers `.env`, `config.json`, and `*.local.yml` before the first commit.
- `docker-compose.yml` and `Caddyfile` in the repo use placeholders only (`hello.example.com`, `alice@example.com`). The real ones live on the VPS.
- `.env.example` documents `TYC_SMTP_PASSWORD=` with an empty value.
- Base images in the Dockerfile are pinned by digest. GitHub Actions are pinned by commit SHA. Dependabot config keeps both fresh.
- README tells operators to pin the image by version tag or digest, never `latest`, and to avoid auto-updaters. It also documents building locally on the VPS as the zero-trust alternative.
- Commits use a GitHub noreply author address.

## CI (`.github/workflows/`)

Two workflows, both with `permissions: contents: read` at the top and
nothing more unless a job needs it.

**`ci.yml`** on push and pull request:
1. `actions/checkout`, `actions/setup-go` from `go.mod`.
2. `go vet ./...`, `go test -race ./...`.
3. `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` (pinned).
4. `docker build .` to prove the image builds. Nothing is pushed.

**`release.yml`** on push of a `v*` tag only:
1. Same test job as above; publish depends on it passing.
2. `docker/login-action` to GHCR with `GITHUB_TOKEN`, job permission `packages: write`.
3. `docker/build-push-action` for `linux/amd64` and `linux/arm64`, tagged with the semver version tags only (no `latest`), labelled with the source commit.
4. Never triggered by `pull_request` or `pull_request_target`, so a fork cannot produce an image.

## README contents

1. What it is, and how `forward_auth` gates Jellyfin.
2. Host prerequisites: Docker, TCP 80/443 and UDP 443. No ipset or iptables.
3. Config reference and SMTP provider examples.
4. Deployment: edit compose and Caddyfile, `.env`, `docker compose up -d`.
5. Upgrading from v0.1, security notes, Jellyfin known proxies.
6. User instructions: open the portal on a phone on the household Wi-Fi,
   request link, confirm from the same network.

## Build steps and verification

1. `go mod init`, `config.go` with tests for: env only, file only, env overrides file, missing required field named in error, malformed duration → `go test ./...` green.
2. `tokens.go` with tests for issue/peek/consume, expiry, single use, rate-limit window → green.
3. `allow.go` with tests for add/allowed, expiry, refresh, refusal of non-public IPv4, persistence across reload, expired entries dropped on load, 0600 atomic write, missing and corrupt files → green.
4. `handlers.go` with `httptest` tests: form renders, unknown email gets same response as known, XFF ignored from untrusted RemoteAddr, XFF honoured from trusted proxy CIDR, verify GET does not consume, verify POST unlocks once then rejects reuse, `/check` 200 only for an unlocked address and 403 locked page otherwise (including IPv6), `/static/pico.classless.min.css` returns 200 with `text/css` → green.
5. `mail.go`, thin; tested manually against a real SMTP account, plus a unit test that the message body contains the link.
6. `main.go`, Dockerfile, compose file, Caddyfile, `.gitignore`, `.env.example`, Dependabot, both workflows → `docker build` succeeds, `docker compose config` and `caddy validate` pass.
7. On the VPS: deploy, confirm Jellyfin shows the locked page from an unverified network, walk the flow from a phone, confirm the entry in `/data/allowlist.json` and that Jellyfin loads on the Apple TV.
8. `go vet`, `govulncheck` (run via `go run`, not vendored) clean.
