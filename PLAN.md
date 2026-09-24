# takeyourcoat — plan

A tiny Go captive portal that lets a trusted person add their household's
public IPv4 address to an `ipset` list for a few days, unlocking Jellyfin
for every device on that network. Stdlib only. One binary, one JSON file,
one Docker container.

## Decisions (settled)

| Topic         | Decision |
|---------------|----------|
| Placement     | Runs on the public VPS, same host as ipset/iptables. Jellyfin lives at home behind a WireGuard tunnel to that VPS. |
| Container     | `network_mode: host`, `cap_drop: ALL`, `cap_add: NET_ADMIN`, read-only rootfs, `no-new-privileges`. Runs as root inside the container because no-new-privileges blocks file capabilities on exec. Binary execs `ipset` from the image against the host kernel. |
| Ingress       | Caddy on the VPS terminates TLS for `hello.mydomain.com` and proxies to the app on `127.0.0.1:8080`. App trusts `X-Forwarded-For` only from configured proxy addresses. |
| Email         | SMTP with STARTTLS via `net/smtp` + `crypto/tls`. |
| Config        | Environment variables first, optional JSON file second. Every key has a `TYC_` env var. Compose `environment:` block can configure everything with no file mounted. |
| Verification  | Magic link opens a page showing the detected IP with a confirm button. Only the POST whitelists. Defeats link prefetchers. |
| Address family| IPv4 only. IPv6 clients get a clear error. |
| Defaults      | Whitelist 72h, token 15m single use, 3 link requests per email per hour, identical response for known and unknown emails. |
| Dependencies  | None outside the Go standard library. Pico CSS v2.1.1 (MIT, classless build) is vendored as a single file in `static/` and embedded in the binary. |

## Open item to confirm at build time

**Port layout on the VPS.** The firewall rule matches on destination
port, so Jellyfin and the portal need different ports on the same public
IP. Recommended layout:

- `hello.mydomain.com` → Caddy :443 → app `127.0.0.1:8080`. Always open.
- `jellyfin.mydomain.com:8920` → Caddy :8920 → `10.x.x.x:8096` over WireGuard. Gated by ipset.

Host rules (documented in README, not run by the app):

```sh
ipset create jellyfin_clients hash:ip family inet timeout 259200
iptables -A INPUT -p tcp --dport 8920 -m set ! --match-set jellyfin_clients src -j DROP
```

If you would rather keep Jellyfin on 443, the alternative is a second
public IP on the VPS. Say so and the plan adapts; the Go code does not
change either way.

## Request flow

```
GET  /               → email form
POST /request        → if email trusted and under rate limit: mint token, send mail (async)
                       → always render "if that address is trusted, a link is on its way"
GET  /verify?token=  → look up token (not consumed) → show detected IPv4 + confirm button
POST /verify         → consume token → ipset add <set> <ip> timeout <ttl> -exist → success page
```

No cookies, no sessions, no database. The token is the only credential.

## Security properties

- **Client IP**: `netip.ParseAddr` on the last `X-Forwarded-For` hop only when `RemoteAddr` is a trusted proxy; otherwise `RemoteAddr`. Must be IPv4, must not be private, loopback, link-local or multicast. Anything else is rejected before touching ipset.
- **Command execution**: `exec.Command("ipset", "add", set, ip, "timeout", ttl, "-exist")` with a fixed argv. No shell. The IP argument is the `String()` of a parsed `netip.Addr`, never user text.
- **Tokens**: 32 bytes from `crypto/rand`, base64url in the link. Server stores `sha256(token)` → {email, expiry}. Single use, purged on expiry.
- **Enumeration and timing**: same page and status for known and unknown emails. Mail is sent in a goroutine so response time does not reveal whether a send happened.
- **Rate limiting**: per-email sliding window (default 3/hour) so the portal cannot be used to spam the trusted list. Requests for unknown emails do no work.
- **HTTP hygiene**: `http.Server` with Read/Write/Idle timeouts, `MaxBytesReader` on forms, method checks, `html/template` for all output, `Cache-Control: no-store` on pages, `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, CSP `default-src 'none'; style-src 'self'; form-action 'self'`. No inline styles, no scripts, no external requests from the browser. Listens on loopback only by default.
- **Container**: runs as root with every capability dropped except NET_ADMIN, read-only rootfs and no-new-privileges. A non-root user with setcap on ipset was rejected because no-new-privileges makes the kernel ignore file capabilities on exec.
- **Secrets**: SMTP password arrives via env from a 0600 `.env` file, or via the JSON file. It is never logged and the config struct has no `String()` that could leak it. Logs record email and IP on success only.
- **Refresh semantics**: `-exist` means re-verifying resets the 72h timer rather than erroring.

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
| `TYC_TRUSTED_PROXIES`            | `trusted_proxies`              | `127.0.0.1,::1`    |
| `TYC_TRUSTED_EMAILS`             | `trusted_emails`               | required           |
| `TYC_IPSET_NAME`                 | `ipset_name`                   | `jellyfin_clients` |
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
any missing or malformed field, naming the field. Emails are compared
case-insensitively after trimming. The password is never logged.

Equivalent JSON, for anyone who prefers a file:

```json
{
  "public_url": "https://hello.mydomain.com",
  "trusted_emails": ["alice@example.com", "bob@example.com"],
  "smtp": {
    "host": "smtp.fastmail.com",
    "username": "you@fastmail.com",
    "password": "app-password",
    "from": "Jellyfin Access <you@fastmail.com>"
  }
}
```

## Code layout

Single `package main`, roughly 350 lines across five files. Function list
is the whole API surface:

| File          | Functions |
|---------------|-----------|
| `main.go`     | `main` — load config, build mux (handlers + embedded static), run server with timeouts and graceful shutdown |
| `config.go`   | `loadConfig` (file then env), `Duration.UnmarshalJSON`, `applyEnv` (one `os.LookupEnv` per key) |
| `handlers.go` | `handleIndex`, `handleRequest`, `handleVerifyGet`, `handleVerifyPost`, `clientIP` |
| `tokens.go`   | `tokenStore.issue`, `tokenStore.peek`, `tokenStore.consume`, `limiter.allow` — mutex-guarded maps, expiry checked on access |
| `mail.go`     | `sendMagicLink` — STARTTLS dial, `smtp.PlainAuth`, plain-text body |
| `ipset.go`    | `ipsetAdd` — a package-level `var runIpset = exec.Command…` so tests substitute a fake |

HTML lives as `html/template` string constants in `handlers.go`; four
short pages sharing one layout template, no JavaScript. Styling comes
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
  with `apk add --no-cache ipset`, running as root with only NET_ADMIN.
  Image size: about 18 MB (alpine + ipset + libcap). Published as `ghcr.io/<you>/takeyourcoat`
  via a GitHub Actions workflow on tag push, so the VPS only pulls.
- `docker-compose.yml`, complete and self-contained:

```yaml
services:
  takeyourcoat:
    image: ghcr.io/<you>/takeyourcoat:latest
    network_mode: host
    cap_drop: [ALL]
    cap_add: [NET_ADMIN]
    read_only: true
    security_opt: [no-new-privileges:true]
    restart: unless-stopped
    environment:
      TYC_PUBLIC_URL: https://hello.mydomain.com
      TYC_TRUSTED_EMAILS: alice@example.com,bob@example.com
      TYC_IPSET_NAME: jellyfin_clients
      TYC_WHITELIST_TTL: 72h
      TYC_SMTP_HOST: smtp.fastmail.com
      TYC_SMTP_USERNAME: you@fastmail.com
      TYC_SMTP_PASSWORD: ${TYC_SMTP_PASSWORD}
      TYC_SMTP_FROM: Jellyfin Access <you@fastmail.com>
```

  The password comes from a `.env` file next to the compose file, mode
  0600, gitignored. No volume mount is needed. Anyone who prefers a JSON
  file adds `TYC_CONFIG=/etc/takeyourcoat/config.json` and a read-only
  volume.

## Public repository hygiene

The repo and image are public. The design assumes attackers read the
code; what must not leak is the deployment.

- `.gitignore` covers `.env`, `config.json`, and `*.local.yml` before the first commit.
- `docker-compose.yml` in the repo uses placeholders only (`hello.example.com`, `alice@example.com`). The real compose file lives on the VPS.
- `.env.example` documents `TYC_SMTP_PASSWORD=` with an empty value.
- Base images in the Dockerfile are pinned by digest. GitHub Actions are pinned by commit SHA. Dependabot config keeps both fresh.
- README tells operators to pin the image by version tag or digest, never `latest`, and to avoid auto-updaters against a NET_ADMIN container. It also documents building locally on the VPS as the zero-trust alternative.
- Commits use a GitHub noreply author address.

## CI (`.github/workflows/`)

Two workflows, both with `permissions: contents: read` at the top and
nothing more unless a job needs it.

**`ci.yml`** on push and pull request:
1. `actions/checkout`, `actions/setup-go` from `go.mod`.
2. `go vet ./...`, `go test -race ./...`.
3. `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
4. `docker build .` to prove the image builds. Nothing is pushed.

**`release.yml`** on push of a `v*` tag only:
1. Same test job as above; publish depends on it passing.
2. `docker/login-action` to GHCR with `GITHUB_TOKEN`, job permission `packages: write`.
3. `docker/build-push-action` for `linux/amd64` and `linux/arm64`, tagged with the version and `latest`, labelled with the source commit.
4. Never triggered by `pull_request` or `pull_request_target`, so a fork cannot produce an image.

## README contents

1. Host prerequisites: ipset create, iptables rule, persistence via
   `ipset save` and `iptables-save` on reboot (or netfilter-persistent).
   Security notes from the section above.
2. Caddyfile snippet for both hostnames.
3. WireGuard note: Caddy on the VPS proxies to the home peer's tunnel IP.
4. Config reference and SMTP provider examples.
5. `docker compose up -d`.
6. User instructions: open the portal on a phone on the household Wi-Fi,
   request link, confirm from the same network.

## Build steps and verification

1. `go mod init`, `config.go` with tests for: env only, file only, env overrides file, missing required field named in error, malformed duration → `go test ./...` green.
2. `tokens.go` with tests for issue/peek/consume, expiry, single use, rate-limit window → green.
3. `ipset.go` with a test asserting the exact argv produced and that a non-IPv4 or private address is refused → green.
4. `handlers.go` with `httptest` tests: form renders, unknown email gets same response as known, XFF ignored from untrusted RemoteAddr, XFF honoured from trusted proxy, verify GET does not consume, verify POST calls fake ipset once then rejects reuse, `/static/pico.classless.min.css` returns 200 with `text/css` → green.
5. `mail.go` — thin; tested manually against a real SMTP account, plus a unit test that the message body contains the link.
6. `main.go`, Dockerfile, compose file, `.gitignore`, `.env.example`, Dependabot, both workflows → `docker build` succeeds, `docker compose up` on a Mac with env-only config and a fake `ipset` shim renders all four pages.
7. On the VPS: create the ipset, deploy, walk the flow from a phone, confirm with `ipset list jellyfin_clients` and that Jellyfin loads on the Apple TV.
8. `go vet`, `govulncheck` (run via `go run`, not vendored) clean.
