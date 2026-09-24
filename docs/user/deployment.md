# Deployment

[Operator guide](README.md) > Deployment

Prerequisites: Docker and open ports from [VPS setup](vps-setup.md), and the
WireGuard tunnel from [Caddy and WireGuard](caddy-and-wireguard.md).

## The compose file, service by service

Keep your real copy on the VPS only, for example in `/opt/takeyourcoat/`,
next to your edited `Caddyfile`. The ones in the repository use placeholders.

```yaml
services:
  caddy:
    # caddy:2.11.4
    image: caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config
    networks:
      web:
        # Fixed address so the app can trust exactly this proxy and nothing else.
        ipv4_address: 172.28.0.10

  takeyourcoat:
    # Pin to a version tag or digest, never latest.
    image: ghcr.io/nmbradley/takeyourcoat:0.2.0
    # Or build from source on the VPS instead of pulling:
    # build: .
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - tyc-data:/data
    networks: [web]
    environment:
      TYC_LISTEN: 0.0.0.0:8080
      TYC_TRUSTED_PROXIES: 172.28.0.10
      TYC_PUBLIC_URL: https://hello.example.com
      TYC_TRUSTED_EMAILS: alice@example.com,bob@example.com
      TYC_WHITELIST_TTL: 72h
      TYC_SMTP_HOST: smtp.resend.com
      TYC_SMTP_USERNAME: resend
      TYC_SMTP_PASSWORD: ${TYC_SMTP_PASSWORD}
      TYC_SMTP_FROM: Jellyfin Access <hello@example.com>

networks:
  web:
    ipam:
      config:
        - subnet: 172.28.0.0/24

volumes:
  caddy-data:
  caddy-config:
  tyc-data:
```

### `caddy`

| Line | Why |
|------|-----|
| `image: caddy:2@sha256:...` | The official Caddy image, pinned by digest; the comment above records the version (`caddy:2.11.4`). Dependabot updates the digest in the repository; review and copy it to your VPS. See [Pinning](#pinning-the-image). |
| `ports: 80, 443, 443/udp` | The only published ports. 80 for certificates and redirects, 443 TCP for HTTPS, 443 UDP for HTTP/3. |
| `./Caddyfile:/etc/caddy/Caddyfile:ro` | Your Caddyfile, read-only. See [Caddy and WireGuard](caddy-and-wireguard.md#caddyfile). |
| `caddy-data:/data` | Certificates and ACME account keys. Keep this volume, or Caddy requests new certificates on every recreate and can hit rate limits. |
| `caddy-config:/config` | Caddy's autosaved config. |
| `networks: web: ipv4_address: 172.28.0.10` | Shares the `web` network with the app, so `takeyourcoat:8080` resolves, at a fixed address the app can trust exactly. |

Caddy is part of the compose project because `forward_auth` makes it part of
the design: it has to reach the app by name on every Jellyfin request.

### `takeyourcoat`

| Line | Why |
|------|-----|
| `image: ...` | A fixed release, never `latest`. See [Pinning](#pinning-the-image). |
| `# build: .` | Swap in to build from a reviewed checkout instead. See [Building locally](#building-locally-on-the-vps). |
| `read_only: true` | The root filesystem is read-only. The only writable path is the `/data` volume. |
| `cap_drop: [ALL]` | No Linux capabilities at all. The app binds an unprivileged port, writes one file it owns and makes outbound SMTP connections; none of that needs a capability. |
| `security_opt: [no-new-privileges:true]` | No process in the container can gain privileges on exec, through setuid binaries or file capabilities. |
| `tyc-data:/data` | The allowlist, `/data/allowlist.json`. See [Configuration](configuration.md#the-state-file). |
| `networks: [web]` | Reachable from Caddy as `takeyourcoat`. No `ports:` entry, so nothing outside the compose network can connect to it. |
| `TYC_LISTEN: 0.0.0.0:8080` | Listen on the container's network interface so Caddy can reach it. The default, `127.0.0.1`, would only be reachable from inside the container. |
| `TYC_TRUSTED_PROXIES: 172.28.0.10` | Believe `X-Forwarded-For` only from Caddy's fixed address, not from the whole `web` network: the network's gateway, `172.28.0.1`, is the host itself. See [Trusted proxies](configuration.md#trusted-proxies). |
| `environment:` (the rest) | The configuration. See [Configuration](configuration.md). |
| `TYC_SMTP_PASSWORD: ${TYC_SMTP_PASSWORD}` | Interpolated by Compose from `.env` so the secret is not in the compose file. |

There is no `user:` line: the image already runs as UID 65532
(`USER 65532:65532` in the Dockerfile). There is no `cap_add` and no
`network_mode: host`. In v0.1 both were needed so the app could change the
host's ipset; now the app only answers Caddy, so it needs neither the host's
network namespace nor any privilege.

Settings not listed use their defaults: state file `/data/allowlist.json`,
15 minute links, 3 links per email per client address per hour, SMTP port 587.

### `networks` and `volumes`

| Block | Why |
|-------|-----|
| `web` with `subnet: 172.28.0.0/24` | A fixed subnet, so Caddy can have a fixed address in it (`172.28.0.10`) for `TYC_TRUSTED_PROXIES` to name. Without it Docker picks a range, and it can differ between hosts. If `172.28.0.0/24` clashes with something on your VPS, pick another private range and change the subnet, Caddy's `ipv4_address` and `TYC_TRUSTED_PROXIES` together. |
| `caddy-data`, `caddy-config`, `tyc-data` | Named volumes, managed by Docker. They survive `docker compose down` and upgrades; `docker compose down -v` deletes them. |

## The `.env` file

Next to the compose file:

```sh
cd /opt/takeyourcoat
cp .env.example .env
chmod 600 .env
${EDITOR:-nano} .env
```

It holds a single line:

```sh
TYC_SMTP_PASSWORD=your-app-password
```

Mode `0600` keeps other users on the VPS from reading it. If `.env` is missing
or the value is empty, the app refuses to start with
`TYC_SMTP_PASSWORD (smtp.password): required`.

If you did not clone the repository onto the VPS, create `.env` directly:

```sh
install -m 600 /dev/null /opt/takeyourcoat/.env
${EDITOR:-nano} /opt/takeyourcoat/.env
```

## Start

```sh
cd /opt/takeyourcoat
sudo docker compose up -d
sudo docker compose logs takeyourcoat caddy
```

A healthy app start logs one line:

```text
2026/09/24 12:00:00 listening on 0.0.0.0:8080
```

Caddy logs that it obtained certificates for both hostnames. A successful
unlock logs:

```text
2026/09/24 12:05:31 unlocked 203.0.113.5 for alice@example.com
```

Then prove the gate: from a network that has not verified,
`curl -s -o /dev/null -w '%{http_code}\n' https://jellyfin.example.com` must
print `403`. Walk the flow from a phone on that network and it should print
Jellyfin's own status. See
[VPS setup](vps-setup.md#3-two-ways-it-can-go-wrong).

## Pinning the image

Images are published to `ghcr.io/nmbradley/takeyourcoat` when a `v*` tag is
pushed. The release workflow produces these tags for git tag `v0.2.0`:

| Image tag | Moves? |
|-----------|--------|
| `0.2.0` | No, one release |
| `0.2` | Yes, follows the newest `0.2.x` |

There is no `latest` tag.

Git tags keep the leading `v` (`v0.2.0`); image tags do not (`0.2.0`),
because the workflow uses the semver `{{version}}` pattern. The sample compose
file pins `:0.2.0`.

- **Pin a full version** (`:0.2.0`) at minimum. Never use `0.2`.
- **Better, pin the digest**, which cannot be re-pointed even if the tag is:

  ```sh
  sudo docker buildx imagetools inspect ghcr.io/nmbradley/takeyourcoat:0.2.0
  ```

  Copy the top-level `Digest:` value into the compose file:

  ```yaml
      image: ghcr.io/nmbradley/takeyourcoat:0.2.0@sha256:<digest>
  ```

  The shipped compose file already pins Caddy this way (`caddy:2@sha256:...`).
  To move to a newer Caddy yourself, inspect `caddy:2` the same way and
  replace the digest and the version comment.

- **No auto-updaters** (Watchtower and similar) against either container. Caddy
  holds your TLS keys and decides who reaches Jellyfin; review each upgrade by
  hand.

## Building locally on the VPS

The zero-trust alternative to pulling a published image: build it yourself
from source you have read.

```sh
sudo git clone https://github.com/nmbradley/takeyourcoat.git /opt/takeyourcoat
cd /opt/takeyourcoat
git log -1
```

Review the code, then in `docker-compose.yml` comment out `image:` and
uncomment `build: .`. Build and start:

```sh
sudo docker compose build
sudo docker compose up -d
```

The build context excludes `.env*`, `docker-compose*.yml`, `Caddyfile`, `docs/`
and `.git` (see `.dockerignore`), so your secrets and hostnames never enter the
image. The Dockerfile's base
images are pinned by digest, so the same commit always builds on the same bases.

## Upgrading

1. Read the release notes and the diff since your current version.
2. Change the tag (and digest) in `docker-compose.yml`, or `git pull` and
   review if you build locally.
3. Pull or build, then recreate:

   ```sh
   cd /opt/takeyourcoat
   sudo docker compose pull
   sudo docker compose up -d
   ```

   For local builds use `sudo docker compose up -d --build` instead.

4. Check the log for `listening on`.

What survives a restart or upgrade: unlocked addresses (in
`/data/allowlist.json` on the `tyc-data` volume) and Caddy's certificates (on
`caddy-data`). What does not: outstanding email links and rate-limit counters
(they live in memory). Anyone mid-flow simply requests a new link.

## Viewing logs

```sh
cd /opt/takeyourcoat
sudo docker compose logs -f takeyourcoat
```

| Log line | Meaning |
|----------|---------|
| `listening on 0.0.0.0:8080` | Started. |
| `unlocked <ip> for <email>` | Someone unlocked a network. |
| `unlocked <ip> for <email>, replacing <old>` | Someone unlocked a new network; their previous address `<old>` is locked again. |
| `send mail to <email>: <error>` | The link was generated but the email failed. See [Troubleshooting](troubleshooting.md#mail-never-arrives). |
| `allowlist add <ip>: <error>` | The unlock could not be saved, usually a permission problem on `/data`. See [Troubleshooting](troubleshooting.md#state-file-permission-denied). |
| `allowlist <path>: <error>; moving it to <path>.corrupt and starting empty` | The state file was corrupt. It was renamed aside and every household must verify again. See [Configuration](configuration.md#the-state-file). |
| `allowlist: <error>` | Refused to start: the state file could not be read, or a corrupt one could not be moved aside. See [Troubleshooting](troubleshooting.md#state-file-permission-denied). |
| `config: <field>: <problem>` | Refused to start. See [Configuration](configuration.md#validation). |

`/check` requests are not logged; Caddy makes one per Jellyfin request. The
SMTP password is never logged. Requests for unknown email addresses are not
logged at all.

## Migrating from v0.1

v0.1 gated Jellyfin with an ipset set and an iptables rule on port 8920, with
the app on host networking holding `NET_ADMIN` and Caddy on the host. v0.2
drops all of that.

1. Stop the old stack: `sudo docker compose down` in `/opt/takeyourcoat`.
2. Stop the host Caddy so ports 80 and 443 are free:
   `sudo systemctl disable --now caddy`. Its Caddyfile is replaced by the one
   in the repository.
3. Remove the host firewall pieces, rule first, then the set:

   ```sh
   sudo iptables -D INPUT -p tcp --dport 8920 -m set ! --match-set jellyfin_clients src -j DROP
   sudo ip6tables -D INPUT -p tcp --dport 8920 -j DROP
   sudo ipset destroy jellyfin_clients
   sudo netfilter-persistent save
   ```

   Saving matters: otherwise the old rule and set come back at boot. If you
   installed `ipset-persistent` only for this, you can remove it.
4. Replace `docker-compose.yml` with the new one and copy your settings across.
   **Remove `TYC_IPSET_NAME`**; the app refuses to start while it is set. No
   new setting is required: `TYC_LISTEN`, `TYC_TRUSTED_PROXIES` and the
   `tyc-data` volume are already in the new file.
5. Put the repository's `Caddyfile` next to it and edit the two hostnames and
   the Jellyfin backend address (see
   [Caddy and WireGuard](caddy-and-wireguard.md#caddyfile)). Jellyfin no longer
   needs a port of its own: the site is `jellyfin.example.com`, not
   `jellyfin.example.com:8920`.
6. `sudo docker compose up -d`, then check both logs.
7. In the cloud firewall, open UDP 443 and close TCP 8920.
8. Change the server address in every Jellyfin client from
   `https://jellyfin.example.com:8920` to `https://jellyfin.example.com`.
9. Check Jellyfin's **Known proxies** still matches the address it sees
   connections from ([Caddy and WireGuard](caddy-and-wireguard.md#jellyfin-known-proxies)).
10. Existing unlocks are lost, because they lived in the ipset. Tell your
    trusted people to verify once more.
