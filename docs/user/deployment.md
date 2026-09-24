# Deployment

[Operator guide](README.md) > Deployment

Prerequisites: the set, the firewall rule and Caddy from
[VPS setup](vps-setup.md) and [Caddy and WireGuard](caddy-and-wireguard.md).

## The compose file, line by line

Keep your real copy on the VPS only, for example in `/opt/takeyourcoat/`. The
one in the repository uses placeholders.

```yaml
services:
  takeyourcoat:
    # Pin to a version tag or digest, never latest.
    image: ghcr.io/nmbradley/takeyourcoat:0.1.0
    # Or build from source on the VPS instead of pulling:
    # build: .
    network_mode: host
    cap_drop: [ALL]
    cap_add: [NET_ADMIN]
    read_only: true
    security_opt: [no-new-privileges:true]
    restart: unless-stopped
    environment:
      TYC_PUBLIC_URL: https://hello.example.com
      TYC_TRUSTED_EMAILS: alice@example.com,bob@example.com
      TYC_IPSET_NAME: jellyfin_clients
      TYC_WHITELIST_TTL: 72h
      TYC_SMTP_HOST: smtp.fastmail.com
      TYC_SMTP_USERNAME: you@example.com
      TYC_SMTP_PASSWORD: ${TYC_SMTP_PASSWORD}
      TYC_SMTP_FROM: Jellyfin Access <you@example.com>
```

| Line | Why |
|------|-----|
| `image: ...` | A fixed release, never `latest`. See [Pinning](#pinning-the-image). |
| `# build: .` | Swap in to build from a reviewed checkout instead. See [Building locally](#building-locally-on-the-vps). |
| `network_mode: host` | ipset entries live in a network namespace. Host networking puts `ipset add` in the host's namespace, where your firewall rule reads the set. It also lets the app listen on the host's `127.0.0.1:8080`, where host Caddy reaches it, without publishing a port to the internet. |
| `cap_drop: [ALL]` | Starts from zero Linux capabilities. |
| `cap_add: [NET_ADMIN]` | The one capability `ipset add` needs. Nothing else is granted, so the container cannot, for example, bind privileged ports, change file ownership or load kernel modules. |
| `read_only: true` | The root filesystem is read-only. The app writes nothing to disk, and nobody can drop a replacement `ipset` binary into the image at run time. |
| `security_opt: [no-new-privileges:true]` | No process in the container can gain privileges on exec, through setuid binaries or file capabilities. The app already holds everything it will ever get: `NET_ADMIN`. |
| `restart: unless-stopped` | Comes back after crashes and reboots. |
| `environment:` | The whole configuration. See [Configuration](configuration.md). |
| `TYC_SMTP_PASSWORD: ${TYC_SMTP_PASSWORD}` | Interpolated by Compose from `.env` so the secret is not in the compose file. |

Settings not listed use their defaults: listen on `127.0.0.1:8080`, trust
`127.0.0.1` and `::1` as proxies, 15 minute links, 3 links per email per hour,
SMTP port 587.

The app runs as **root inside the container, on purpose**. With
`no-new-privileges` the kernel ignores file capabilities on exec, so a
non-root user plus `setcap` on ipset could never gain `NET_ADMIN`. Root here
holds exactly one capability: inside the container `/proc/self/status` shows
`Uid` 0 with `CapEff` and `CapBnd` both `0000000000001000` (`NET_ADMIN` only).
It cannot bypass file permissions, load modules or use any other capability.
Do not add a `user:` line; see
[Troubleshooting](troubleshooting.md#ipset-add-permission-denied).
`EXPOSE 8080` in the Dockerfile has no effect under host networking.

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
sudo docker compose logs takeyourcoat
```

A healthy start logs one line:

```text
2026/09/24 12:00:00 listening on 127.0.0.1:8080
```

A successful unlock logs:

```text
2026/09/24 12:05:31 whitelisted 203.0.113.5 for alice@example.com
```

Then walk the flow from a phone on a household network and confirm with
`sudo ipset list jellyfin_clients` and the `curl` test in
[VPS setup](vps-setup.md#6-the-four-ways-the-rule-silently-does-nothing).

## Pinning the image

Images are published to `ghcr.io/nmbradley/takeyourcoat` when a `v*` tag is
pushed. The release workflow produces these tags for git tag `v0.1.0`:

| Image tag | Moves? |
|-----------|--------|
| `0.1.0` | No, one release |
| `0.1` | Yes, follows the latest `0.1.x` |
| `latest` | Yes, follows every release |

Git tags keep the leading `v` (`v0.1.0`); image tags do not (`0.1.0`),
because the workflow uses the semver `{{version}}` pattern. The sample compose
file pins `:0.1.0`.

- **Pin a full version** (`:0.1.0`) at minimum. Never use `latest` or `0.1`.
- **Better, pin the digest**, which cannot be re-pointed even if the tag is:

  ```sh
  sudo docker buildx imagetools inspect ghcr.io/nmbradley/takeyourcoat:0.1.0
  ```

  Copy the top-level `Digest:` value into the compose file:

  ```yaml
      image: ghcr.io/nmbradley/takeyourcoat:0.1.0@sha256:<digest>
  ```

- **No auto-updaters** (Watchtower and similar) against this container. It has
  `NET_ADMIN` on the host network namespace; review each upgrade by hand.

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

The build context excludes `.env*`, `docker-compose*.yml` and `.git` (see
`.dockerignore`), so your secrets never enter the image. The Dockerfile's base
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

What survives a restart: entries in the ipset (they live in the kernel). What
does not: outstanding email links and rate-limit counters (they live in
memory). Anyone mid-flow simply requests a new link.

## Viewing logs

```sh
cd /opt/takeyourcoat
sudo docker compose logs -f takeyourcoat
```

| Log line | Meaning |
|----------|---------|
| `listening on 127.0.0.1:8080` | Started. |
| `whitelisted <ip> for <email>` | Someone unlocked a network. |
| `send mail to <email>: <error>` | The link was generated but the email failed. See [Troubleshooting](troubleshooting.md#mail-never-arrives). |
| `ipset add <ip>: ipset [add ...]: exit status 1: <ipset output>` | The unlock failed on the host side. See [Troubleshooting](troubleshooting.md). |
| `config: <field>: <problem>` | Refused to start. See [Configuration](configuration.md#validation). |

The SMTP password is never logged. Requests for unknown email addresses are
not logged at all.
