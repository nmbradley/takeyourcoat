# Security model

[Operator guide](README.md) > Security model

takeyourcoat reduces **who can reach** Jellyfin. It does not decide **who is
logged in** to Jellyfin. Keep Jellyfin's own accounts as the real access
control.

## What it protects against

| Threat | How |
|--------|-----|
| Internet-wide scanners and bots finding and probing Jellyfin | Caddy asks the app (`forward_auth` to `/check`) before every Jellyfin request and returns a `403` locked page for every address not unlocked. Jellyfin itself never sees those requests. |
| Brute force and exploits against Jellyfin from arbitrary hosts | Same: only requests from unlocked household addresses are proxied to Jellyfin. |
| Strangers unlocking their own network | Only addresses in `TYC_TRUSTED_EMAILS` get a link, and only someone who can read that inbox can use it. |
| Discovering who is on the trusted list | The portal gives the same page and status for trusted, unknown and rate-limited addresses, and sends mail in the background so timing does not reveal a send. |
| Spamming trusted inboxes through the portal | At most `TYC_REQUESTS_PER_EMAIL_PER_HOUR` links (3) per email per client address per rolling hour, and four times that (12) per email across all addresses. Requests from IPv6 or private addresses send nothing. |
| Link-preview bots and mail scanners burning the link | Opening a link only shows a confirm page. The address is added only when Unlock (a POST) is pressed. |
| Stolen or replayed links | Links are random 32-byte tokens, work once, and expire after `TYC_TOKEN_TTL` (15 minutes). The server stores only a hash of each token. |
| One inbox unlocking many networks | Each trusted email holds one unlocked address at a time; verifying from a new network locks the old one. |
| Clickjacking and downgrade | Pages forbid framing (`frame-ancestors 'none'`) and send `Strict-Transport-Security`. |
| Spoofed client addresses | `X-Forwarded-For` is believed only from addresses inside `TYC_TRUSTED_PROXIES` (the compose network), and only the last hop (the one Caddy added). Only public IPv4 addresses are ever unlocked. |
| Command injection | The app runs no commands and has no shell to run them with. The state file is JSON written from parsed addresses, never request text. |
| Probing other households' status | `/check` answers only allowed or not, and only for the caller's own address. |
| Your home IP being exposed | Jellyfin is reached over WireGuard from the VPS; only the VPS is public. |

## What it does not protect against

- **Anyone on an unlocked network.** Guests on the household Wi-Fi, a
  compromised smart TV, or a neighbour sharing the connection can all reach
  Jellyfin's login page until the entry expires.
- **A compromised trusted inbox.** Whoever reads the email can unlock their own
  network.
- **Attacks on Jellyfin itself from unlocked addresses.** The portal is not a
  web application firewall.
- **A compromised VPS.** Root on the VPS controls Caddy, its TLS keys, the
  allowlist and the tunnel.
- **A misconfigured Caddyfile.** The gate is only the `forward_auth` block. If
  it is removed, Jellyfin is open (see
  [VPS setup](vps-setup.md#3-two-ways-it-can-go-wrong)).
- **IPv6 users.** The design is IPv4 only. IPv6 clients are not let in; they
  get the locked page.

## Accepted limits

| Limit | What it means |
|-------|---------------|
| **An IP is a household, not a person** | Unlocking admits every device behind that public address for up to 72 hours. |
| **Carrier-grade NAT** | Some ISPs and most mobile carriers put many customers behind one public IPv4. Unlocking such an address admits every customer sharing it. Ask users to verify only from home broadband, never from mobile data. |
| **The inbox is the root of trust** | Trusted email accounts are effectively keys. Losing one to phishing means someone else can unlock a network. |
| **Userspace enforcement, not a kernel drop** | v0.1 dropped unverified packets in the kernel, so Jellyfin's port looked closed. Now every client completes TLS with Caddy and is refused with a `403`. Scanners can see that `jellyfin.example.com` exists and serves a locked page, Caddy's own code is exposed to every client, and a bug in Caddy or the app, or a Caddyfile mistake, could let requests through where a firewall rule would not. In exchange there is no privileged container, no host firewall to get wrong, and Jellyfin shares port 443. |
| **Unprivileged container** | The app runs as UID 65532 with no capabilities, a read-only root filesystem, `no-new-privileges` and no host networking. A bug that let an attacker run code in it could add entries to the allowlist, and so unlock networks, or read the SMTP password. It could not touch the host's network, firewall or files. |
| **Quota exhaustion of a known email** | The limit is per email and client address, so a stranger cannot use up the owner's 3 links from home. But anyone who knows a trusted address and uses several addresses can use up its cap of 12 per hour, and the owner's request is then silently ignored until the hour rolls over. |
| **One network per person** | Someone who unlocks from a relative's house moves their unlock there, and their own home is locked until they verify at home again. Give a household two trusted addresses if it needs both. |
| **Tokens in access logs** | The link is `GET /verify?token=...`. If Caddy access logging is on for the portal, tokens land in the log. They are single use and expire in 15 minutes, but treat those logs as sensitive or leave access logging off. |
| **State in memory** | Outstanding links and rate-limit counters are lost on restart. Unlocks are not; they are in `/data/allowlist.json`. |
| **No revocation UI** | To lock out an address early, delete the state file and restart; everyone re-verifies (see [Configuration](configuration.md#the-state-file)). |

## Operational advice

- **2FA on GitHub** for anyone who can push tags to the repository, since a
  tag push publishes an image.
- **2FA on every trusted email account.**
- **Pin the images by digest**, never `latest`, and do not run auto-updaters
  against either container. Or build locally from reviewed source. See
  [Deployment](deployment.md#pinning-the-image).
- **Keep Jellyfin authentication strong**: unique passwords, no passwordless
  users, admin accounts not used for everyday viewing, and Jellyfin kept up to
  date. Set up [Known proxies](caddy-and-wireguard.md#jellyfin-known-proxies) so
  Jellyfin's failed-login handling sees real client addresses.
- **Keep the trusted list short** and remove people who no longer need access.
- **Keep secrets on the VPS.** `.env` at mode `0600`; the real compose file,
  Caddyfile and any `config.json` never go into a repository.
- **Review the allowlist now and then**:
  `sudo docker compose exec takeyourcoat cat /data/allowlist.json`.
