# Architecture overview

[Operator guide](README.md) > Architecture overview

takeyourcoat is a minimal, self-hosted replacement for
[Knocknoc](https://knocknoc.io): a trusted email address stands in for the
identity provider, and Caddy's `forward_auth` stands in for firewall
orchestration, so whatever Caddy fronts is reachable only from networks a
trusted person has unlocked.

## The flow in plain words

1. Someone on your trusted list, say `alice@example.com`, picks up a phone that
   is connected to her **home Wi-Fi** (not mobile data) and opens
   `https://hello.example.com`.
2. She types her email address and taps **Send link**. The page always says a
   link is on its way, whether or not the address is on the list.
3. If the address is trusted and has not asked too often (3 times per hour
   from her network by default), the portal emails her a link that works once and expires after
   15 minutes.
4. She opens the link on the same phone, still on home Wi-Fi. The page shows
   the public IPv4 address the portal sees and an **Unlock** button. Opening
   the link does nothing by itself, so mail scanners that prefetch links cannot
   use it up.
5. She taps **Unlock**. The portal adds that address to its allowlist under
   her email with a 72 hour expiry and saves the list to
   `/data/allowlist.json`. Each email unlocks one network at a time: if she
   later verifies from somewhere else, her unlock moves there.
6. Her TV, console or any device on the household network opens
   `https://app.example.com`. Before proxying, Caddy asks the portal
   `GET /check` with the device's address (the household address) in
   `X-Forwarded-For`. It is on the list, so the portal answers `200` and Caddy
   passes the request through the tunnel to the backend. Every other device in
   her house gets the same answer.
7. A device on any other network gets `403` and a page saying "This network is
   not unlocked", with a link to the portal. The backend never sees the request.
8. After 72 hours the entry expires. Doing the flow again before then resets
   the timer, and is also what to do when her home IP changes.

## The pieces

```
          Alice's home network (public IP 203.0.113.5)
   +-------------------------------------------------------+
   |  phone ---- Wi-Fi router ---- TV, console, tablet ... |
   +-------------------------------------------------------+
        |  HTTPS :443 hello.example.com
        |  HTTPS :443 app.example.com
        v
   +----------------------------- VPS ---------------------------------+
   |   compose network "web" (172.28.0.0/24), Caddy at 172.28.0.10     |
   |   +---------------+  hello: reverse_proxy   +--------------------+ |
   |   |  Caddy        | ----------------------> | takeyourcoat :8080 | |
   |   |  :80 :443     |                         | UID 65532, no caps | |
   |   |               |  app: forward_auth      | /data/allowlist    | |
   |   |               | ---- GET /check ------> |   .json (volume)   | |
   |   |               | <--- 200 ok / 403 ----- |                    | |
   |   +---------------+                         +--------------------+ |
   |        | on 200 only: reverse_proxy               | SMTP :587      |
   |        | 10.0.0.2:8080                            v                |
   |        v                                     your mail provider    |
   |   wg0 (10.0.0.1) on the host                                       |
   +--------|----------------------------------------------------------+
            |  WireGuard tunnel
            v
   +-------------- your home server ----------------+
   |  wg0 (10.0.0.2)  ->  your backend :8080         |
   |  firewall: accept 8080 only from 10.0.0.1       |
   +-------------------------------------------------+
```

- **Caddy** terminates TLS for both hostnames on 443. The portal hostname
  proxies to the app. The protected hostname first runs `forward_auth` against
  the app's `/check`, and only on a `200` proxies through the tunnel.
- **takeyourcoat** serves the portal pages, the stylesheet and `/check`, sends
  mail, and keeps the allowlist in memory and in one JSON file on a volume. It
  runs unprivileged, has no published port, and executes no commands.
- **WireGuard** carries the backend's traffic from the VPS to your home, so your
  home IP is never published and your home router needs no open ports.

See [VPS setup](vps-setup.md) and [Caddy and WireGuard](caddy-and-wireguard.md)
to build it, and [Security model](security-model.md) for what this does and
does not protect.
