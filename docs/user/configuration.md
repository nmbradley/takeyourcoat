# Configuration

[Operator guide](README.md) > Configuration

Everything is set with `TYC_*` environment variables. In the normal Docker
deployment there is no config file: the settings live in the compose file's
`environment:` block and the SMTP password in `.env`
(see [Deployment](deployment.md)).

## Settings

Defaults are the values in `loadConfig` in `config.go`.

| Env var | JSON key | Default | Meaning |
|---------|----------|---------|---------|
| `TYC_LISTEN` | `listen` | `127.0.0.1:8080` | Address the HTTP server listens on. The shipped compose file sets `0.0.0.0:8080`, because the app is on a bridge network with Caddy and publishes no port. |
| `TYC_PUBLIC_URL` | `public_url` | required | Base URL used to build the emailed link and the locked page's link, for example `https://hello.example.com`. Must be `https://` (plain `http://` only for `localhost` or `127.0.0.1`). |
| `TYC_TRUSTED_PROXIES` | `trusted_proxies` | `127.0.0.1,::1` | Direct peers whose `X-Forwarded-For` is believed: addresses or CIDR prefixes. The shipped compose file sets `172.28.0.0/24`. |
| `TYC_TRUSTED_EMAILS` | `trusted_emails` | required | Who may request a link. Bare addresses only (`alice@example.com`, not `Alice <alice@example.com>`). Compared lower-cased and trimmed. |
| `TYC_STATE_FILE` | `state_file` | `/data/allowlist.json` | Where the allowlist is saved. See [The state file](#the-state-file). |
| `TYC_WHITELIST_TTL` | `whitelist_ttl` | `72h` | How long an unlocked address stays unlocked. |
| `TYC_TOKEN_TTL` | `token_ttl` | `15m` | How long an emailed link stays valid. |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR` | `requests_per_email_per_hour` | `3` | Links sent per email address **from one client address** in any rolling hour. Each email is also capped at four times this (12) per hour across all client addresses. |
| `TYC_SMTP_HOST` | `smtp.host` | required | SMTP server name. Also used for TLS certificate checks. |
| `TYC_SMTP_PORT` | `smtp.port` | `587` | SMTP port. Must offer STARTTLS. |
| `TYC_SMTP_USERNAME` | `smtp.username` | required | SMTP login. |
| `TYC_SMTP_PASSWORD` | `smtp.password` | required | SMTP password or app password. Never logged. |
| `TYC_SMTP_FROM` | `smtp.from` | required | From address, for example `Jellyfin Access <hello@example.com>`. |
| `TYC_CONFIG` | none | unset | Path to an optional JSON file, read before the environment. |

`TYC_IPSET_NAME` (v0.1) is gone. If it is set in the environment at all, even
to an empty value, the app refuses to start with
`TYC_IPSET_NAME: no longer used; v0.2 gates via Caddy forward_auth, see README`.
An `ipset_name` key left in a JSON file is ignored like any unknown key.

### Formats

- **Lists** (`TYC_TRUSTED_PROXIES`, `TYC_TRUSTED_EMAILS`) are comma-separated.
  Spaces around items are trimmed and empty items are dropped, so
  `alice@example.com, bob@example.com` is fine. In JSON they are arrays.
- **Durations** are Go duration strings: `72h`, `90m`, `1h30m`, `45s`. There is
  no `d` unit, so three days is `72h`. In JSON they are strings (`"72h"`), not
  numbers.
- **Numbers** (`TYC_SMTP_PORT`, `TYC_REQUESTS_PER_EMAIL_PER_HOUR`) are plain
  integers.
- **Setting a variable to an empty value counts as setting it.**
  `TYC_TRUSTED_PROXIES=` means "trust no proxy", and an empty
  `TYC_SMTP_PASSWORD` (for example when `.env` is missing) fails validation.

### Notes on specific settings

- `TYC_WHITELIST_TTL` is the lifetime of each unlock. Re-verifying sets the
  expiry to now plus this value again. The success page shows the TTL in whole
  hours, so values under `1h` display as 0.
- **One network per email.** Each trusted email holds at most one unlocked
  address. Verifying from a new network moves that person's unlock there and
  locks the previous address (logged as `unlocked <new> for <email>, replacing
  <old>`), unless someone else's email has unlocked it too. If two people
  unlock the same address, it is recorded under whoever verified last.
- The rate limit window is fixed at one hour; only the count is configurable.

### Trusted proxies

`TYC_TRUSTED_PROXIES` must cover the address Caddy connects from. Each entry
is either a bare address, which means exactly that address, or a CIDR prefix:

| Entry | Trusts |
|-------|--------|
| `127.0.0.1` | only `127.0.0.1` (same as `127.0.0.1/32`) |
| `::1` | only `::1` (same as `::1/128`) |
| `172.28.0.0/24` | `172.28.0.0` to `172.28.0.255`, the shipped compose network |
| `172.28.0.0/24,127.0.0.1` | both of the above |

In the shipped compose file Caddy and the app share the `web` network, pinned
to `172.28.0.0/24`, so Caddy's container address is always in that range even
though Docker may hand it a different address on each recreate. If you change
the subnet in the compose file, change `TYC_TRUSTED_PROXIES` with it.

If Caddy connects from an address that is not covered, the app sees Caddy's
own private address as the client: `/check` returns the locked page for
everyone and the portal refuses the link with the IPv6 or private address
message. Keep the range tight: every host inside it can claim any client
address.

### The state file

Unlocked addresses are kept in memory and saved to `TYC_STATE_FILE` after
every unlock, so they survive restarts and upgrades.

- **Format**: one JSON object; keys are IPv4 addresses, values hold the email
  that unlocked it and the expiry time in RFC 3339, UTC:

  ```json
  {"203.0.113.9":{"email":"alice@example.com","expires":"2026-09-27T01:00:00Z"}}
  ```

- **Where it lives**: `/data/allowlist.json` inside the container. `/data` is
  the only writable path; the shipped compose file mounts the named volume
  `tyc-data` there. To look at it:

  ```sh
  sudo docker compose exec takeyourcoat cat /data/allowlist.json
  ```

- **Writes** go to a temporary file in `/data` (`.allowlist-<random>`), which
  is flushed to disk, set to mode `0600` and renamed over the real file, so a
  crash never leaves a half-written file. If the write fails, the unlock fails
  and nothing changes in memory either.
- **Expired entries** are dropped on load and whenever the list is read.
  Entries for addresses that are not public IPv4 are dropped on load.
- **Ownership**: the app runs as UID 65532. A new named volume copies the
  image's `/data`, which is owned by 65532, so it works as is. If you use a
  bind mount instead, `sudo chown 65532:65532` the host directory.
- **Safe to delete.** The app starts with an empty list and every household
  verifies once more:

  ```sh
  sudo docker compose exec takeyourcoat rm /data/allowlist.json
  sudo docker compose restart takeyourcoat
  ```

  Removing the whole volume (`sudo docker compose down`, then
  `sudo docker volume rm <project>_tyc-data`) does the same.
- **Corrupt file.** If the file is not valid JSON, or has a key that is not an
  address or a value without a valid `email`/`expires` object, the app logs
  `allowlist /data/allowlist.json: <error>; moving it to
  /data/allowlist.json.corrupt and starting empty`, renames it aside and
  starts with an empty list. Everyone verifies again; the `.corrupt` file is
  kept for you to inspect or delete. Only an unreadable file (or a failed
  rename) stops startup, with `allowlist: <error>`.
- There is no revocation UI and no way to remove a single address. To lock
  out an address early, delete the file as above; everyone else then
  verifies again too.

## Optional JSON file

Set `TYC_CONFIG` to a file path. The file is read first, then environment
variables override any key they set. Either source alone is enough. Unknown
keys in the file are ignored.

```json
{
  "public_url": "https://hello.example.com",
  "trusted_emails": ["alice@example.com", "bob@example.com"],
  "trusted_proxies": ["172.28.0.0/24"],
  "state_file": "/data/allowlist.json",
  "whitelist_ttl": "72h",
  "token_ttl": "15m",
  "requests_per_email_per_hour": 3,
  "smtp": {
    "host": "smtp.resend.com",
    "port": 587,
    "username": "resend",
    "password": "api-key",
    "from": "Jellyfin Access <hello@example.com>"
  }
}
```

The container has a read-only root filesystem, so mount the file read-only and
point `TYC_CONFIG` at it:

```yaml
    environment:
      TYC_CONFIG: /etc/takeyourcoat/config.json
    volumes:
      - ./config.json:/etc/takeyourcoat/config.json:ro
```

The app runs as UID 65532 with no capabilities, so it can only read the file
if that user may. Give it to that user and keep it private:

```sh
sudo chown 65532:65532 /opt/takeyourcoat/config.json
sudo chmod 600 /opt/takeyourcoat/config.json
```

## Validation

The config is checked once at startup. On any problem the process exits
before listening, and the message names each bad field by env var and JSON
key. What is rejected:

| Field | Rejected when |
|-------|---------------|
| `TYC_PUBLIC_URL` | empty, or not an absolute `https://` URL with a host (`http://` is accepted only for `localhost` and `127.0.0.1`) |
| `TYC_TRUSTED_EMAILS` | no non-empty entries, or any entry that is not a bare email address (`invalid address "<value>"`) |
| `TYC_TRUSTED_PROXIES` | any entry that is neither an IP address nor a CIDR prefix (`invalid address or CIDR "<value>"`) |
| `TYC_SMTP_HOST`, `TYC_SMTP_USERNAME`, `TYC_SMTP_PASSWORD` | empty |
| `TYC_SMTP_FROM` | empty, or not parseable as an email address |
| `TYC_SMTP_PORT` | not an integer, or outside 1-65535 |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR` | not an integer, or less than 1 |
| `TYC_WHITELIST_TTL`, `TYC_TOKEN_TTL` | not a Go duration, or less than `1s` |
| `TYC_LISTEN`, `TYC_STATE_FILE` | empty |
| `TYC_IPSET_NAME` | set at all (checked first, before anything else) |
| `TYC_CONFIG` | file unreadable, or not valid JSON for these types |

`TYC_LISTEN` is not otherwise checked here; a bad address fails when the server
tries to listen. `TYC_STATE_FILE` is read right after validation: an
unreadable file stops the app with `allowlist: <error>`; a corrupt one is
moved aside (see [The state file](#the-state-file)). A missing file is fine. Whether the directory is writable only shows on the first
unlock (see [Troubleshooting](troubleshooting.md#state-file-permission-denied)).

What the errors look like in `docker compose logs`:

```text
2026/09/24 12:00:00 config: TYC_SMTP_PASSWORD (smtp.password): required
TYC_TRUSTED_EMAILS (trusted_emails): required
```

```text
2026/09/24 12:00:00 config: TYC_WHITELIST_TTL: time: unknown unit " days" in duration "3 days"
TYC_SMTP_PORT: strconv.Atoi: parsing "abc": invalid syntax
```

```text
2026/09/24 12:00:00 config: /etc/takeyourcoat/config.json: time: invalid duration "soon"
```

Errors in environment values are reported first; once those are fixed, the
remaining field checks run. Several problems of the same kind are listed
together, one per line.

## SMTP

Mail is sent with plain SMTP, upgraded with **STARTTLS**, then authenticated
with `PLAIN`. It fails closed: if the server does not offer STARTTLS or the
certificate does not match `TYC_SMTP_HOST`, nothing is sent and the error is
logged.

- Use **port 587** (submission with STARTTLS). This is the default.
- **Port 465 is not supported.** It expects TLS from the first byte (implicit
  TLS), which this client does not do; the send times out or fails.
- The connection attempt times out after 10 seconds and the whole send after
  30 seconds.

### Provider examples

**Fastmail**

```yaml
      TYC_SMTP_HOST: smtp.fastmail.com
      TYC_SMTP_USERNAME: you@example.com
      TYC_SMTP_FROM: Jellyfin Access <you@example.com>
```

Password: an app password from Settings > Privacy & Security > App passwords.

**Gmail**

```yaml
      TYC_SMTP_HOST: smtp.gmail.com
      TYC_SMTP_USERNAME: you@example.com
      TYC_SMTP_FROM: Jellyfin Access <you@example.com>
```

Password: an app password (requires 2-Step Verification on the Google
account). The normal account password will not work.

**Amazon SES**

```yaml
      TYC_SMTP_HOST: email-smtp.eu-west-1.amazonaws.com
      TYC_SMTP_USERNAME: <SES SMTP username>
      TYC_SMTP_FROM: Jellyfin Access <you@example.com>
```

Use your region's endpoint. Username and password are SES SMTP credentials,
not IAM access keys, and `TYC_SMTP_FROM` must be a verified identity. While the
account is in the SES sandbox, recipients must be verified too.

**Resend**

```yaml
      TYC_SMTP_HOST: smtp.resend.com
      TYC_SMTP_USERNAME: resend
      TYC_SMTP_FROM: Jellyfin Access <hello@example.com>
```

The username is the literal word `resend`. The password is an API key; create
one with sending permission only and scope it to the one domain. That domain
must be verified in the Resend dashboard, which means adding the DKIM and SPF
records it gives you, and `TYC_SMTP_FROM` must be an address on it.

In every case `TYC_SMTP_PASSWORD` goes in `.env`, not in the compose file.
