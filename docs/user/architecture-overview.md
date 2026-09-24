# Architecture overview

[Operator guide](README.md) > Architecture overview

## The flow in plain words

1. Someone on your trusted list, say `alice@example.com`, picks up a phone that
   is connected to her **home Wi-Fi** (not mobile data) and opens
   `https://hello.example.com`.
2. She types her email address and taps **Send link**. The page always says a
   link is on its way, whether or not the address is on the list.
3. If the address is trusted and has not asked too often (3 times per hour by
   default), the portal emails her a link that works once and expires after
   15 minutes.
4. She opens the link on the same phone, still on home Wi-Fi. The page shows
   the public IPv4 address the portal sees and an **Unlock** button. Opening
   the link does nothing by itself, so mail scanners that prefetch links cannot
   use it up.
5. She taps **Unlock**. The portal runs `ipset add` to put that address in the
   `jellyfin_clients` set with a 72 hour timeout.
6. The VPS firewall drops traffic to the Jellyfin port (8920) from any address
   that is not in the set. Her address now is, so the Apple TV, tablet and
   laptop in her house can all reach `https://jellyfin.example.com:8920`.
7. After 72 hours the kernel removes the entry on its own. Doing the flow again
   before then resets the timer, and is also what to do when her home IP
   changes.

## The pieces

```
          Alice's home network (public IP 203.0.113.5)
   +-------------------------------------------------------+
   |  phone ---- Wi-Fi router ---- Apple TV, tablet, ...    |
   +-------------------------------------------------------+
        |  HTTPS :443                     |  HTTPS :8920
        |  (portal, always open)          |  (Jellyfin, gated)
        v                                 v
   +----------------------------- VPS ------------------------------+
   |                                                                |
   |   iptables INPUT:  tcp dport 8920, src NOT in jellyfin_clients |
   |                    -> DROP                                     |
   |        |                               ^                       |
   |        v                               | ipset add ... timeout |
   |   +---------+   127.0.0.1:8080   +-------------------------+   |
   |   |  Caddy  | -----------------> | takeyourcoat container  |   |
   |   |  :443   |                    | (host network,          |   |
   |   |  :8920  |                    |  NET_ADMIN only)        |   |
   |   +---------+                    +-------------------------+   |
   |        |                               |                       |
   |        | 10.0.0.2:8096                 | SMTP STARTTLS :587    |
   |        v                               v                       |
   |   wg0 (10.0.0.1)                  your mail provider           |
   +--------|-------------------------------------------------------+
            |  WireGuard tunnel
            v
   +-------------- your home server ----------------+
   |  wg0 (10.0.0.2)  ->  Jellyfin :8096             |
   |  firewall: accept 8096 only from 10.0.0.1       |
   +-------------------------------------------------+
```

- **Caddy** terminates TLS for both hostnames. The portal hostname proxies to
  the app on loopback; the Jellyfin hostname proxies through the tunnel.
- **takeyourcoat** serves four page routes and one stylesheet, sends mail, and runs exactly one
  command: `ipset add <set> <ip> timeout <seconds> -exist`.
- **ipset and iptables** are yours. You create the set and the rule once; the
  app only adds entries.
- **WireGuard** carries Jellyfin traffic from the VPS to your home, so your
  home IP is never published and your home router needs no open ports.

See [VPS setup](vps-setup.md) and [Caddy and WireGuard](caddy-and-wireguard.md)
to build it, and [Security model](security-model.md) for what this does and
does not protect.
