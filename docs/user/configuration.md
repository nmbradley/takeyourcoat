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
| `TYC_LISTEN` | `listen` | `127.0.0.1:8080` | Address the HTTP server listens on. Keep it on loopback behind Caddy. |
| `TYC_PUBLIC_URL` | `public_url` | required | Base URL used to build the emailed link, for example `https://hello.example.com`. |
| `TYC_TRUSTED_PROXIES` | `trusted_proxies` | `127.0.0.1,::1` | Direct peers whose `X-Forwarded-For` is believed. |
| `TYC_TRUSTED_EMAILS` | `trusted_emails` | required | Who may request a link. Compared lower-cased and trimmed. |
| `TYC_IPSET_NAME` | `ipset_name` | `jellyfin_clients` | Set that addresses are added to. Must already exist. |
| `TYC_WHITELIST_TTL` | `whitelist_ttl` | `72h` | How long an unlocked address stays in the set. |
| `TYC_TOKEN_TTL` | `token_ttl` | `15m` | How long an emailed link stays valid. |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR` | `requests_per_email_per_hour` | `3` | Links sent per email address in any rolling hour. |
| `TYC_SMTP_HOST` | `smtp.host` | required | SMTP server name. Also used for TLS certificate checks. |
| `TYC_SMTP_PORT` | `smtp.port` | `587` | SMTP port. Must offer STARTTLS. |
| `TYC_SMTP_USERNAME` | `smtp.username` | required | SMTP login. |
| `TYC_SMTP_PASSWORD` | `smtp.password` | required | SMTP password or app password. Never logged. |
| `TYC_SMTP_FROM` | `smtp.from` | required | From address, for example `Jellyfin Access <you@example.com>`. |
| `TYC_CONFIG` | none | unset | Path to an optional JSON file, read before the environment. |

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

- `TYC_WHITELIST_TTL` becomes the `timeout` argument to `ipset add`, in whole
  seconds. ipset limits timeouts to 2147483 seconds (about 24 days). The
  success page shows the TTL in whole hours, so values under `1h` display as 0.
- `TYC_TRUSTED_PROXIES` must include the address Caddy connects from. With
  Caddy on the host and the app on host networking, that is `127.0.0.1`, which
  is the default. If Caddy connects from somewhere else and is not listed, the
  app sees Caddy's own address as the client and refuses it as private.
- The rate limit window is fixed at one hour; only the count is configurable.

## Optional JSON file

Set `TYC_CONFIG` to a file path. The file is read first, then environment
variables override any key they set. Either source alone is enough. Unknown
keys in the file are ignored.

```json
{
  "public_url": "https://hello.example.com",
  "trusted_emails": ["alice@example.com", "bob@example.com"],
  "trusted_proxies": ["127.0.0.1", "::1"],
  "ipset_name": "jellyfin_clients",
  "whitelist_ttl": "72h",
  "token_ttl": "15m",
  "requests_per_email_per_hour": 3,
  "smtp": {
    "host": "smtp.fastmail.com",
    "port": 587,
    "username": "you@example.com",
    "password": "app-password",
    "from": "Jellyfin Access <you@example.com>"
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

The app runs as root in the container but with every capability except
`NET_ADMIN` dropped, so it cannot bypass file permissions. Keep the file owned
by root and readable only by its owner:

```sh
sudo chown root:root /opt/takeyourcoat/config.json
sudo chmod 600 /opt/takeyourcoat/config.json
```

## Validation

The config is checked once at startup. On any problem the process exits
before listening, and the message names each bad field by env var and JSON
key. What is rejected:

| Field | Rejected when |
|-------|---------------|
| `TYC_PUBLIC_URL` | empty, or not an absolute `http://` or `https://` URL with a host |
| `TYC_TRUSTED_EMAILS` | no non-empty entries |
| `TYC_TRUSTED_PROXIES` | any entry that is not an IP address (CIDR ranges are not accepted) |
| `TYC_SMTP_HOST`, `TYC_SMTP_USERNAME`, `TYC_SMTP_PASSWORD` | empty |
| `TYC_SMTP_FROM` | empty, or not parseable as an email address |
| `TYC_SMTP_PORT` | not an integer, or outside 1-65535 |
| `TYC_REQUESTS_PER_EMAIL_PER_HOUR` | not an integer, or less than 1 |
| `TYC_WHITELIST_TTL`, `TYC_TOKEN_TTL` | not a Go duration, or less than `1s` |
| `TYC_LISTEN`, `TYC_IPSET_NAME` | empty |
| `TYC_CONFIG` | file unreadable, or not valid JSON for these types |

`TYC_LISTEN` is not otherwise checked here; a bad address fails when the server
tries to listen. `TYC_IPSET_NAME` is not checked against the host; a wrong
name shows up as an `ipset add` error on the first unlock.

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

In every case `TYC_SMTP_PASSWORD` goes in `.env`, not in the compose file.
