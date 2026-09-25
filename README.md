# takeyourcoat

takeyourcoat is a minimal, self-hosted replacement for
[Knocknoc](https://knocknoc.io): just-in-time network access without a VPN or a
client. A person proves control of a trusted email address, and the public IPv4
address they are on is unlocked for a limited time. Caddy asks takeyourcoat on
every request whether the client is unlocked, so whatever Caddy fronts is
invisible to everyone else. One Go binary, no dependencies, one container, one
JSON file.

A trusted person opens the portal from their home network, enters their email
and clicks a magic link; every device on that network (TV, console, tablet,
phone) can then reach the protected site for a few days. It is **exposure
reduction, not authentication**: it hides your backend from the internet at
large, and the backend's own login remains the real access control. Both
hostnames, the portal and the protected site, are served by Caddy on port 443.

## Compared with Knocknoc

Knocknoc is a commercial product ("Remove Attack Surface & Network Exposure")
that keeps services hidden until a user authenticates through an identity
provider, then orchestrates firewalls, security groups and WAFs to grant
just-in-time access to that user's IP. takeyourcoat does the same job at
household scale:

- **Identity**: a magic link sent to a trusted email address, instead of SSO
  and MFA through an identity provider.
- **Enforcement**: Caddy's `forward_auth` in front of one host, instead of
  orchestrating firewalls, security groups and WAFs.
- **Scale**: a household or a small team, not an organisation.
- **Scope**: no dashboard, no audit UI, no integrations. The allowlist is a
  JSON file with a TTL.
- **Cost**: free, MIT licensed, about 800 lines of Go.

## How it works

1. `hello.example.com` is always open. Caddy proxies it to the app.
2. A trusted person requests a link (`POST /request`), opens it on the same
   network (`GET /verify`) and presses Unlock (`POST /verify`). The app adds
   their public IPv4 address to its allowlist, stored in a JSON file on a
   volume, for `TYC_WHITELIST_TTL` (72 hours by default).
3. For every request to `app.example.com`, Caddy's `forward_auth` first
   sends `GET /check` to the app with the client address in
   `X-Forwarded-For`.
4. Unlocked address: the app answers `200` and Caddy proxies the request to
   the backend, for example over WireGuard. Anything else: the app answers
   `403` with a short "this network is not unlocked" page, and Caddy returns
   that page to the client. The backend never sees the request.

## Host prerequisites

- Docker with the Compose plugin.
- Inbound TCP 80, TCP 443 and UDP 443 open in the cloud firewall and in the
  host firewall. Port 80 is for certificates and the HTTPS redirect; UDP 443 is
  HTTP/3.

Nothing else. **ipset and iptables are no longer needed**: there is no set to
create, no rule to write and nothing to persist across reboots. IPv6 is still
not supported, but it no longer needs blocking: an IPv6 client simply gets the
locked page.

## Configuration

Everything is set with `TYC_*` environment variables. Optionally, `TYC_CONFIG`
names a JSON file that is read first; environment variables override it.
Lists are comma-separated, durations are Go duration strings (`72h`, `15m`).
The process refuses to start on any missing or malformed field and names it.

| Env var                          | JSON key                       | Default                |
|----------------------------------|--------------------------------|------------------------|
| `TYC_LISTEN`                     | `listen`                       | `127.0.0.1:8080`       |
| `TYC_PUBLIC_URL`                 | `public_url`                   | required               |
| `TYC_TRUSTED_PROXIES`            | `trusted_proxies`              | `127.0.0.1,::1`        |
| `TYC_TRUSTED_EMAILS`             | `trusted_emails`               | required               |
| `TYC_STATE_FILE`                 | `state_file`                   | `/data/allowlist.json` |
| `TYC_WHITELIST_TTL`              | `whitelist_ttl`                | `72h`                  |
| `TYC_TOKEN_TTL`                  | `token_ttl`                    | `15m`                  |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR`| `requests_per_email_per_hour`  | `3`                    |
| `TYC_SMTP_HOST`                  | `smtp.host`                    | required               |
| `TYC_SMTP_PORT`                  | `smtp.port`                    | `587`                  |
| `TYC_SMTP_USERNAME`              | `smtp.username`                | required               |
| `TYC_SMTP_PASSWORD`              | `smtp.password`                | required               |
| `TYC_SMTP_FROM`                  | `smtp.from`                    | required               |

- `TYC_LISTEN`: the shipped compose file sets `0.0.0.0:8080`, because the app
  sits on a bridge network with Caddy. No port is published, so it is still
  unreachable from outside.
- `TYC_TRUSTED_PROXIES` accepts addresses and CIDR prefixes. In the shipped
  compose file Caddy has the fixed address `172.28.0.10` on the `web` network
  (`172.28.0.0/24`), and the app trusts exactly that address.
- `TYC_PUBLIC_URL` must be `https://` (plain `http://` only for `localhost`).
- `TYC_REQUESTS_PER_EMAIL_PER_HOUR` counts per email and client address, with
  a cap of four times that per email across all addresses.
- `TYC_STATE_FILE` is where the allowlist is kept. The compose file puts
  `/data` on a named volume.
- `TYC_IPSET_NAME` is gone. If it is set at all, the app refuses to start and
  points here.

### SMTP providers

Mail is sent over SMTP with STARTTLS on port 587.

- **Fastmail**: host `smtp.fastmail.com`, username is your Fastmail address,
  password is an app password (Settings, Privacy & Security, App passwords).
- **Gmail**: host `smtp.gmail.com`, username is your Gmail address, password is
  an [app password](https://myaccount.google.com/apppasswords) (requires 2-Step
  Verification). Your normal password will not work.
- **Amazon SES**: host `email-smtp.<region>.amazonaws.com`, username and
  password are SES SMTP credentials (not IAM access keys). `TYC_SMTP_FROM`
  must be a verified identity.
- **Resend**: host `smtp.resend.com`, username is the literal word `resend`,
  password is an API key scoped to sending. `TYC_SMTP_FROM` must be on a
  domain verified in the Resend dashboard.

## Deployment

On the VPS, put [`docker-compose.yml`](docker-compose.yml) and
[`Caddyfile`](Caddyfile) in one directory and edit them:

- In `docker-compose.yml`: your portal URL, trusted emails and SMTP settings.
- In `Caddyfile`: replace `hello.example.com` and `app.example.com` with
  your two hostnames (both need DNS A records pointing at the VPS), and
  `10.0.0.2:8080` with the address and port your backend listens on, as Caddy
  reaches it (for a home server, normally the home peer's WireGuard address).

Then:

```sh
cp .env.example .env
chmod 600 .env
$EDITOR .env          # set TYC_SMTP_PASSWORD
docker compose up -d
```

Caddy obtains certificates for both hostnames on first start. Clients of the
protected site use `https://app.example.com` with no port.

## Upgrading from v0.1

- Remove `TYC_IPSET_NAME` from your compose file or JSON config; the app
  refuses to start while it is set. No new setting is required.
- Replace your compose file with the new one (it now includes Caddy) and add
  the `Caddyfile` next to it.
- Stop the host Caddy (`systemctl disable --now caddy`); Caddy moves from the
  host network into the compose network.
- Delete the iptables and ip6tables rules for port 8920, then the set
  (`ipset destroy <set name>`, the name you had in `TYC_IPSET_NAME`), and save your rules so they do not come
  back at boot.
- The protected site no longer needs its own port. Change client addresses from
  `https://app.example.com:8920` to `https://app.example.com`, and
  close 8920 in the cloud firewall.
- Existing unlocks are lost; each household verifies once more.

Step by step: [Deployment](docs/user/deployment.md#migrating-from-v01).

## Security notes

- **Unprivileged container.** The app runs as UID 65532 with a read-only root
  filesystem, `cap_drop: [ALL]`, `no-new-privileges` and no host networking.
  It writes only its state file on `/data`.
- **Pin the image** by version tag or, better, digest
  (`ghcr.io/nmbradley/takeyourcoat@sha256:...`). Releases publish semver tags
  only (`0.2.0`, `0.2`); there is no `latest`. The shipped compose file pins
  Caddy by digest too (`caddy:2@sha256:...`), and Dependabot's
  `docker-compose` ecosystem keeps that digest current in this repository;
  copy updates into your VPS copy after review.
- **No auto-updaters** (Watchtower and similar) against this stack. Review each
  upgrade.
- **Build locally** as the zero-trust alternative: clone the repo on the VPS,
  review it, and replace `image:` with `build: .` in the compose file.
- **Protect the accounts** that can publish or receive links: enable 2FA on
  GitHub and on every trusted email account. Anyone who can read a trusted
  inbox can unlock the protected site for their own network.
- **The trade-off.** v0.1 dropped unverified packets in the kernel, so
  the backend's port looked closed. Now TLS is completed by Caddy and requests are
  rejected in userspace with a 403. Scanners can see that the hostname exists
  and serves a locked page, and a Caddy or app bug could let requests through
  where a kernel rule would not. In exchange there is no privileged container,
  no host firewall to get wrong, and the protected site shares port 443.
- Keep your real `docker-compose.yml`, `Caddyfile`, `.env` and any
  `config.json` on the VPS only.

## Trusted proxies on your backend

Your backend sees every user arriving from one address: the Caddy
container as it appears to the backend (through a tunnel, usually the VPS
tunnel address). If your backend has brute-force protection or logs client
IPs, tell it to trust the proxy address it sees connections from, so it reads
the real client from `X-Forwarded-For`. Most web servers and applications have a trusted-proxies
setting for this.

## Using it

Send this to the people on your trusted list:

1. On a phone connected to your **home Wi-Fi** (not mobile data), open
   `https://hello.example.com`.
2. Enter your email address and tap the button.
3. Open the link in the email **on the same phone, still on home Wi-Fi**,
   within 15 minutes.
4. Check the address shown and tap **Unlock**.
5. Every device on that Wi-Fi can now reach the protected site for 3 days. Repeat
   to reset the timer, or if your home IP changes. If the site shows "This
   network is not unlocked", do this again.

Each email unlocks one network at a time: verifying somewhere else moves the
unlock there and locks the previous network.

## Documentation

- [Operator guide](docs/user/README.md): VPS setup, Caddy and WireGuard,
  configuration, deployment, troubleshooting, security model.
- [Developer guide](docs/dev/README.md): architecture, security design,
  testing, packaging and CI, design decisions.

## Vendored assets

`static/pico.classless.min.css` is [Pico CSS](https://picocss.com) v2.1.1
(MIT), copied from
`https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.classless.min.css`
and embedded in the binary. SHA-256:
`61207a40ffc02a42d1e50143651c121beab70ed413c934c1ff84fa263ba436b0`. It is
never fetched at build or run time; upgrading is a manual copy plus updating
this note.

## License

MIT
