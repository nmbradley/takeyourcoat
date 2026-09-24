# takeyourcoat operator guide

takeyourcoat is a small web portal that runs on your public VPS in one compose
project with Caddy. A trusted person opens it from their home network, enters
their email address, and confirms a one-time link. The portal then unlocks that
household's public IPv4 address for a few days (72 hours by default). Caddy
serves both the portal and Jellyfin on port 443, and before proxying any
Jellyfin request it asks the portal (`forward_auth` to `GET /check`) whether
the client's address is unlocked. So every device on an unlocked network,
including TVs that cannot log in to anything, can reach Jellyfin at once, and
everyone else gets a short "not unlocked" page. It is **exposure reduction, not
authentication**: it hides Jellyfin from the internet at large, but anyone on
an unlocked network can reach the Jellyfin login page, and Jellyfin's own
accounts and passwords remain the real access control. The portal does not
support IPv6 clients. Unlocks are kept in a small JSON file on a volume;
outstanding links live in memory and are lost on restart.

## Pages

| Page | What it covers |
|------|----------------|
| [Architecture overview](architecture-overview.md) | The flow from phone to Apple TV, with a diagram of the moving parts |
| [VPS setup](vps-setup.md) | Docker, DNS, open ports, the two ways it can go wrong |
| [Caddy and WireGuard](caddy-and-wireguard.md) | Caddyfile with `forward_auth`, tunnel to the home Jellyfin, Jellyfin "Known proxies" |
| [Configuration](configuration.md) | Every `TYC_*` setting, proxy CIDRs, the state file, the optional JSON file, validation errors, SMTP providers |
| [Deployment](deployment.md) | The compose file service by service, `.env`, image pinning, upgrades, logs, migrating from v0.1 |
| [Troubleshooting](troubleshooting.md) | Symptom, cause, fix |
| [Security model](security-model.md) | What it protects against, what it does not, operational advice |

Developers changing the code should start at [the developer docs](../dev/README.md).
