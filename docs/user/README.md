# takeyourcoat operator guide

takeyourcoat is a small web portal that runs on your public VPS next to Caddy.
A trusted person opens it from their home network, enters their email address,
and confirms a one-time link. The portal then adds that household's public IPv4
address to an `ipset` set for a few days (72 hours by default). A firewall rule
that you write only lets addresses in that set reach the Jellyfin port, so
every device on that network, including TVs that cannot log in to anything, is
unlocked at once. It is **exposure reduction, not authentication**: it hides
Jellyfin from the internet at large, but anyone on an unlocked network can
reach the Jellyfin login page, and Jellyfin's own accounts and passwords remain
the real access control. The portal does not create the set or the firewall
rule, does not proxy Jellyfin, does not support IPv6 clients, and keeps no
database: outstanding links live in memory and are lost on restart.

## Pages

| Page | What it covers |
|------|----------------|
| [Architecture overview](architecture-overview.md) | The flow from phone to Apple TV, with a diagram of the moving parts |
| [VPS setup](vps-setup.md) | Docker, ipset, Caddy, the set, the firewall rule, the four pitfalls, persistence |
| [Caddy and WireGuard](caddy-and-wireguard.md) | Caddyfile, port layout, tunnel to the home Jellyfin, Jellyfin "Known proxies" |
| [Configuration](configuration.md) | Every `TYC_*` setting, the optional JSON file, validation errors, SMTP providers |
| [Deployment](deployment.md) | The compose file line by line, `.env`, image pinning, upgrades, logs |
| [Troubleshooting](troubleshooting.md) | Symptom, cause, fix |
| [Security model](security-model.md) | What it protects against, what it does not, operational advice |

Developers changing the code should start at [the developer docs](../dev/README.md).
