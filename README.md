# takeyourcoat

A tiny captive portal for Jellyfin. A trusted person opens the portal from
their home network, enters their email, and clicks a magic link; the portal
adds that household's public IPv4 address to an `ipset` list for a few days.
A host firewall rule only lets addresses in that set reach Jellyfin, so every
device on the network (TV, tablet, phone) is unlocked at once. Go standard
library only, one static binary, one container, no database.

## Host prerequisites

The app only runs `ipset add`. You create the set and the firewall rule on the
VPS yourself:

```sh
ipset create jellyfin_clients hash:ip family inet timeout 259200
iptables -A INPUT -p tcp --dport 8920 -m set ! --match-set jellyfin_clients src -j DROP
```

The portal (`:443`) stays open to everyone; only the Jellyfin port (`8920`) is
gated. Neither the set nor the rule survives a reboot on its own: persist them
with `ipset save` / `iptables-save` restored at boot, or install
`netfilter-persistent` (with `ipset-persistent`) and save after creating them.
The set must exist before the iptables rule is restored.

Four ways this rule can silently do nothing. Check each one after deploying:

- **Caddy in Docker bypasses INPUT.** Traffic to a container's published port
  goes through the FORWARD and DOCKER chains, never INPUT. Run Caddy with
  `network_mode: host`, or put the rule in the `DOCKER-USER` chain instead.
- **Rule order.** `-A` appends after any existing ACCEPT for the port (ufw, a
  previous setup). Use `-I INPUT 1 ...` or confirm with `iptables -L INPUT -n
  --line-numbers` that nothing accepts port 8920 first.
- **IPv6.** The set is IPv4 only. If the Jellyfin hostname has an AAAA record,
  clients connect over IPv6 and skip this rule. Either publish no AAAA record
  for it, or add `ip6tables -A INPUT -p tcp --dport 8920 -j DROP`.
- **Prove it.** From a network that has not verified, `curl -m 5
  https://jellyfin.example.com:8920` must time out. Then verify and try again.

## Caddy and WireGuard

Caddy on the VPS terminates TLS for both hostnames. Jellyfin runs at home and
is reached over a WireGuard tunnel, so Caddy proxies to the home peer's tunnel
IP.

```caddyfile
hello.example.com {
	reverse_proxy 127.0.0.1:8080
}

jellyfin.example.com:8920 {
	reverse_proxy 10.0.0.2:8096
}
```

Replace `10.0.0.2` with your home peer's WireGuard address. Caddy connects from
loopback, which is in the default `TYC_TRUSTED_PROXIES`, so the client IP is
taken from `X-Forwarded-For`.

Through the tunnel, every viewer reaches Jellyfin from the VPS's WireGuard
address. Jellyfin's brute-force lockout would then treat all users as one
client. In Jellyfin, go to Dashboard, Networking, and add the VPS tunnel IP to
"Known proxies" so it reads the real client from `X-Forwarded-For`.

## Configuration

Everything is set with `TYC_*` environment variables. Optionally, `TYC_CONFIG`
names a JSON file that is read first; environment variables override it.
Lists are comma-separated, durations are Go duration strings (`72h`, `15m`).
The process refuses to start on any missing or malformed field and names it.

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

### SMTP providers

Mail is sent over SMTP with STARTTLS on port 587.

- **Fastmail**: host `smtp.fastmail.com`, username is your Fastmail address,
  password is an app password (Settings → Privacy & Security → App passwords).
- **Gmail**: host `smtp.gmail.com`, username is your Gmail address, password is
  an [app password](https://myaccount.google.com/apppasswords) (requires 2-Step
  Verification). Your normal password will not work.
- **Amazon SES**: host `email-smtp.<region>.amazonaws.com`, username and
  password are SES SMTP credentials (not IAM access keys). `TYC_SMTP_FROM`
  must be a verified identity.

## Deployment

On the VPS, next to a copy of [`docker-compose.yml`](docker-compose.yml) edited
with your real hostname, emails and SMTP settings:

```sh
cp .env.example .env
chmod 600 .env
$EDITOR .env          # set TYC_SMTP_PASSWORD
docker compose up -d
```

The container uses `network_mode: host` so it can reach the host's ipset and
listen on loopback, drops all capabilities except `NET_ADMIN`, sets
`no-new-privileges`, and uses a read-only filesystem. The process runs as root
inside the container on purpose: with `no-new-privileges` the kernel ignores
file capabilities on exec, so a non-root user plus `setcap` on ipset would
always fail. Root holds exactly one capability, `NET_ADMIN`, and nothing else.

## Security notes

- **Pin the image** by version tag or, better, digest
  (`ghcr.io/nmbradley/takeyourcoat@sha256:...`). Never use `latest`.
- **No auto-updaters** (Watchtower and similar) against this container. It has
  `NET_ADMIN` on the host network; review each upgrade.
- **Build locally** as the zero-trust alternative: clone the repo on the VPS,
  review it, and replace `image:` with `build: .` in the compose file.
- **Protect the accounts** that can publish or receive links: enable 2FA on
  GitHub and on every trusted email account. Anyone who can read a trusted
  inbox can unlock Jellyfin for their own network.
- Keep your real `docker-compose.yml`, `.env` and any `config.json` on the VPS
  only; they are gitignored here.

## Using it

Send this to the people on your trusted list:

1. On a phone connected to your **home Wi-Fi** (not mobile data), open
   `https://hello.example.com`.
2. Enter your email address and tap the button.
3. Open the link in the email **on the same phone, still on home Wi-Fi**,
   within 15 minutes.
4. Check the address shown and tap **Confirm**.
5. Every device on that Wi-Fi can now reach Jellyfin for 3 days. Repeat to
   reset the timer, or if your home IP changes.

## Vendored assets

`static/pico.classless.min.css` is [Pico CSS](https://picocss.com) v2.1.1
(MIT), copied from
`https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.classless.min.css`
and embedded in the binary. SHA-256:
`61207a40ffc02a42d1e50143651c121beab70ed413c934c1ff84fa263ba436b0`. It is
never fetched at build or run time; upgrading is a manual copy plus updating
this note.

## Documentation

- [Operator guide](docs/user/README.md): VPS setup, Caddy and WireGuard,
  configuration, deployment, troubleshooting, security model.
- [Developer guide](docs/dev/README.md): architecture, security design,
  testing, packaging and CI, design decisions.

## License

MIT
