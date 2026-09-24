# Security model

[Operator guide](README.md) > Security model

takeyourcoat reduces **who can reach** Jellyfin. It does not decide **who is
logged in** to Jellyfin. Keep Jellyfin's own accounts as the real access
control.

## What it protects against

| Threat | How |
|--------|-----|
| Internet-wide scanners and bots finding and probing Jellyfin | The Jellyfin port drops traffic from every address not in the set. To the internet at large it looks closed. |
| Brute force and exploits against Jellyfin from arbitrary hosts | Same: only unlocked household addresses can open a connection. |
| Strangers unlocking their own network | Only addresses in `TYC_TRUSTED_EMAILS` get a link, and only someone who can read that inbox can use it. |
| Discovering who is on the trusted list | The portal gives the same page and status for trusted, unknown and rate-limited addresses, and sends mail in the background so timing does not reveal a send. |
| Spamming trusted inboxes through the portal | At most `TYC_REQUESTS_PER_EMAIL_PER_HOUR` links (3) per address per rolling hour. |
| Link-preview bots and mail scanners burning the link | Opening a link only shows a confirm page. The address is added only when Unlock (a POST) is pressed. |
| Stolen or replayed links | Links are random 32-byte tokens, work once, and expire after `TYC_TOKEN_TTL` (15 minutes). The server stores only a hash of each token. |
| Spoofed client addresses | `X-Forwarded-For` is believed only from `TYC_TRUSTED_PROXIES`, and only the last hop (the one Caddy added). Only public IPv4 addresses are ever passed to `ipset`. |
| Command injection | `ipset` is run with a fixed argument list and no shell; the address argument is re-formatted from a parsed IP, never taken from request text. |
| Your home IP being exposed | Jellyfin is reached over WireGuard from the VPS; only the VPS is public. |

## What it does not protect against

- **Anyone on an unlocked network.** Guests on the household Wi-Fi, a
  compromised smart TV, or a neighbour sharing the connection can all reach
  Jellyfin's login page until the entry expires.
- **A compromised trusted inbox.** Whoever reads the email can unlock their own
  network.
- **Attacks on Jellyfin itself from unlocked addresses.** The portal is not a
  web application firewall.
- **A compromised VPS.** Root on the VPS controls the firewall, Caddy's TLS
  keys and the tunnel.
- **IPv6.** The design is IPv4 only. You must stop IPv6 reaching the Jellyfin
  port yourself (see [VPS setup](vps-setup.md#6-the-four-ways-the-rule-silently-does-nothing)).

## Accepted limits

| Limit | What it means |
|-------|---------------|
| **An IP is a household, not a person** | Unlocking admits every device behind that public address for up to 72 hours. |
| **Carrier-grade NAT** | Some ISPs and most mobile carriers put many customers behind one public IPv4. Unlocking such an address admits every customer sharing it. Ask users to verify only from home broadband, never from mobile data. |
| **The inbox is the root of trust** | Trusted email accounts are effectively keys. Losing one to phishing means someone else can unlock a network. |
| **`NET_ADMIN` blast radius** | The container holds `NET_ADMIN` in the host network namespace. A bug that let an attacker run code in it could flush or fill the set, and, depending on how you resolved the capability setup (see [Troubleshooting](troubleshooting.md#ipset-add-permission-denied)), change host routes or firewall rules. `cap_drop: ALL`, a read-only filesystem and a non-root user (where possible) keep this as small as it can be. |
| **Quota exhaustion of a known email** | Anyone who knows or guesses a trusted address can use up its 3 links per hour, so the real owner's request is silently ignored until the hour rolls over. There is no per-client limit. |
| **Tokens in access logs** | The link is `GET /verify?token=...`. If Caddy access logging is on for the portal, tokens land in the log. They are single use and expire in 15 minutes, but treat those logs as sensitive or leave access logging off. |
| **State in memory** | Outstanding links and rate-limit counters are lost on restart. Set entries in the kernel are not. |
| **No revocation UI** | To lock out an address early, delete it by hand: `sudo ipset del jellyfin_clients 203.0.113.5`. |

## Operational advice

- **2FA on GitHub** for anyone who can push tags to the repository, since a
  tag push publishes an image.
- **2FA on every trusted email account.**
- **Pin the image by digest**, never `latest`, and do not run auto-updaters
  against this container. Or build locally from reviewed source. See
  [Deployment](deployment.md#pinning-the-image).
- **Keep Jellyfin authentication strong**: unique passwords, no passwordless
  users, admin accounts not used for everyday viewing, and Jellyfin kept up to
  date. Set up [Known proxies](caddy-and-wireguard.md#jellyfin-known-proxies) so
  Jellyfin's failed-login handling sees real client addresses.
- **Keep the trusted list short** and remove people who no longer need access.
- **Keep secrets on the VPS.** `.env` at mode `0600`; the real compose file and
  any `config.json` never go into a repository.
- **Review the set now and then**: `sudo ipset list jellyfin_clients`.
