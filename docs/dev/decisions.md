# Decisions

[Developer guide](README.md) > Decisions

Short records of the settled design decisions. Each lists what was chosen,
what was rejected, and why.

## 1. Privileged container on the same host as the firewall

**Decision.** The app runs on the public VPS, in the same network namespace as
the host firewall (`network_mode: host`), with only `NET_ADMIN`, and execs
`ipset` directly.

**Rejected.**

- *Separate privileged helper daemon* (an unprivileged web app talking to a
  root helper over a Unix socket). Two processes, a private protocol, and the
  helper still needs `NET_ADMIN`; the extra boundary protects little when the
  only command is a fixed-argv `ipset add` that already validates its input.
- *SSH from the app to the firewall host* (a forced-command key that runs
  `ipset add`). Needs key management and a second host or loopback SSH, and
  moves input validation into a shell-invoked forced command, which is easier
  to get wrong than `exec.Command` with a parsed address.

**Consequences.** One container, one binary. The container's blast radius is
`NET_ADMIN` in the host network namespace, which is documented as an accepted
limit ([security model](../user/security-model.md#accepted-limits)).

## 2. Separate ports: portal on 443, Jellyfin on 8920

**Decision.** The firewall rule matches on destination port, so the portal
(`hello.example.com:443`) and Jellyfin (`jellyfin.example.com:8920`) share the
VPS's IPv4 address on different ports.

**Rejected.** Jellyfin on 443 with a second public IP. Possible, costs an
extra address, and changes no code; left to operators who want it.

## 3. Confirm button instead of a plain GET

**Decision.** The emailed link (`GET /verify`) only shows the detected address
and an Unlock form. Only `POST /verify` consumes the token and runs ipset.

**Rejected.** Whitelisting on GET. Mail security scanners and link previewers
fetch links automatically; on a plain GET they would burn the single-use token
and, worse, whitelist the scanner's own IP. The confirm page also shows the
user which address is about to be unlocked, which catches "I'm on mobile data".

## 4. IPv4 only

**Decision.** Only public IPv4 addresses are accepted; IPv6 clients get a
clear error page. Operators must keep IPv6 off the Jellyfin port.

**Rejected.** IPv6 support. A household usually has one IPv4 address shared
by every device behind NAT, which is exactly the unit this tool unlocks. With
IPv6 each device has its own addresses, often rotating privacy addresses, so
unlocking the phone's address would not unlock the TV. Doing it properly means
whitelisting a prefix (`/64` or `/56`) with `hash:net`, guessing the right
prefix length per ISP. Not worth the complexity for the use case.

## 5. Environment variables plus optional JSON, not YAML

**Decision.** Every setting has a `TYC_*` env var; an optional JSON file
(`TYC_CONFIG`) is read first and env wins.

**Rejected.** YAML (or TOML). Needs a third-party parser, which conflicts with
standard-library-only. Env vars fit Docker Compose directly, so the normal
deployment has no config file or volume at all. JSON remains for people who
prefer a file, via `encoding/json`.

## 6. SMTP, not an HTTP mail API

**Decision.** Mail is sent with `net/smtp` over STARTTLS, with `PLAIN` auth.

**Rejected.** Provider HTTP APIs (SES API, Postmark, SendGrid, Mailgun). Each
ties the code to one vendor, usually wants its SDK, and needs a long-lived API
key. SMTP submission works with any provider, including personal mailboxes with
an app password, and the stdlib client is enough. Implicit TLS on port 465 was
left out to keep one code path.

## 7. Standard library only

**Decision.** No module dependencies (`go.mod` has no `require`).

**Why.** The binary runs with `NET_ADMIN` on a public host. Every dependency
is code in that process and a supply-chain path into it. Everything needed
(HTTP routing with method patterns, templates, SMTP, `netip`, `embed`) is in
the standard library.

## 8. Pico CSS, vendored and embedded

**Decision.** Pico CSS v2.1.1 classless build is copied into `static/`,
embedded with `//go:embed`, and served from the portal's own origin. Its
SHA-256 is recorded in the README.

**Rejected.**

- *Loading from a CDN.* Would need a CSP exception for another origin, leaks
  every visit to the CDN, and makes page appearance depend on a third party.
- *Fetching at build time.* A network dependency in the build and another
  supply-chain input.
- *Hand-written CSS or no CSS.* Classless Pico makes plain semantic HTML
  readable on phones with no classes, no inline styles and no JavaScript, which
  keeps the CSP at `style-src 'self'`.

## 9. In memory, no database, no sessions

**Decision.** Tokens and rate-limit counters live in process memory; the token
is the only credential; there are no cookies.

**Why.** Restart loss is harmless (users request a new link), and the durable
state that matters, the whitelist, already lives in the kernel with its own
timeouts. No storage means nothing to back up, migrate or leak.

## 10. Go 1.27

**Decision.** `go.mod` requires `go 1.27.0`, and the build image is
`golang:1.27-alpine`.

**Why.** The program is almost entirely standard library (`net/http`,
`net/smtp`, `crypto/tls`, `html/template`), so its vulnerabilities are the
standard library's. Requiring the current release line means builds get the
latest stdlib security fixes and `govulncheck` in CI stays clean.
`GOTOOLCHAIN=auto` fetches the toolchain for developers on older Go.

**Consequence.** Contributors and Dependabot must keep the `go` directive and
the Docker base image on a supported Go release.

## 11. Root in the container rather than non-root plus setcap

**Decision.** The image has no `USER` line; the app runs as root inside the
container. The compose file drops every capability except `NET_ADMIN` and sets
`no-new-privileges`, so root holds exactly `NET_ADMIN` (`CapEff` and `CapBnd`
both `0x1000`).

**Rejected.** A non-root user with `setcap cap_net_admin+ep` on
`/usr/sbin/ipset`, which was the original plan. Under `no-new-privileges` the
kernel refuses to grant capabilities on exec, so ipset would always fail with
"Operation not permitted". Keeping setcap would have meant dropping
`no-new-privileges` instead.

**Why this way round.** `no-new-privileges` closes every exec-time escalation
path (setuid binaries, file capabilities) for the life of the container.
Root without `CAP_DAC_OVERRIDE` and the other capabilities is not
meaningfully stronger than a non-root user, except for the one capability it
needs.

**Consequences.** The Go process itself holds `NET_ADMIN` in the host network
namespace, not only the ipset child. Operators must not add a `user:` line to
the compose file; doing so breaks every unlock
([troubleshooting](../user/troubleshooting.md#ipset-add-permission-denied)).
