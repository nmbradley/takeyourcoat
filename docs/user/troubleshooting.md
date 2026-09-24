# Troubleshooting

[Operator guide](README.md) > Troubleshooting

Start with the logs:

```sh
cd /opt/takeyourcoat
sudo docker compose logs --tail 100 takeyourcoat caddy
```

## Quick reference

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Everyone gets "This network is not unlocked" or the IPv6/private message on Jellyfin, even after unlocking | `TYC_TRUSTED_PROXIES` does not cover the Caddy container, so the app sees Caddy as the client | [Details](#everyone-gets-the-locked-page) |
| Jellyfin reachable from networks that never verified; nobody ever sees the locked page | The Jellyfin site in the Caddyfile has no working `forward_auth` block | [Details](#nobody-gets-the-locked-page) |
| Portal says "Done", but Jellyfin still shows the locked page for that household | Wrong address unlocked, entry expired, or the household IP changed | [Details](#jellyfin-still-locked-after-verifying) |
| Unlocked, the check passes, but Jellyfin does not load | Tunnel or Jellyfin down | [Details](#jellyfin-still-locked-after-verifying) |
| Locked page with the IPv6 message for one client only | The client connected over IPv6; `/check` is IPv4 only | [Details](#ipv6-or-private-address-message) |
| "Something went wrong unlocking your network" and `allowlist add ... permission denied` in the log | `/data` not writable by UID 65532 | [Details](#state-file-permission-denied) |
| Browser shows a certificate error, or Caddy logs ACME failures | Ports 80/443 closed, DNS wrong, or something else on 80/443 | [Details](#caddy-cannot-get-certificates) |
| "That link is invalid or has expired." | Link older than `TYC_TOKEN_TTL`, already used, or the app restarted | [Details](#that-link-is-invalid-or-has-expired) |
| Mail never arrives | Silent by design for unknown or rate-limited addresses; otherwise SMTP errors in the log | [Details](#mail-never-arrives) |
| `curl` to Jellyfin gets a 403 page instead of timing out | Expected in v0.2 | [Details](#curl-gets-a-403-instead-of-a-timeout) |
| Container exits immediately | Config validation failed, `TYC_IPSET_NAME` still set, or the state file is unreadable | Read the `config:` or `allowlist:` line; see [Configuration](configuration.md#validation) |

## Everyone gets the locked page

Every request looks like it comes from a private IP: the app sees the Caddy
container's own address rather than the client's, so `/check` answers `403`
with "This portal only works with public IPv4 addresses", and the emailed link
page shows the same message.

| Cause | Fix |
|-------|-----|
| `TYC_TRUSTED_PROXIES` does not cover the Caddy container's address | Set it to the `web` network's subnet (`172.28.0.0/24` in the shipped file). Find the actual address with `sudo docker compose exec caddy ip -4 addr show eth0`. |
| The `subnet:` in the compose file was changed and `TYC_TRUSTED_PROXIES` was not | Change both together. |
| `TYC_TRUSTED_PROXIES` was set to an empty value, or left at the default `127.0.0.1,::1` | Set it to the compose subnet. The default only fits an app and Caddy on the same host network. |
| The `ipam` block was removed, so Docker picked a different range | Put it back, or set `TYC_TRUSTED_PROXIES` to the range Docker chose (`sudo docker network inspect <project>_web`). |

After changing the compose file: `sudo docker compose up -d`. The address
check happens before the token is touched, so outstanding links still work
once it is fixed.

## Nobody gets the locked page

Test from a network that has not verified:

```sh
curl -s -o /dev/null -w '%{http_code}\n' https://jellyfin.example.com
```

If that prints Jellyfin's status (`200`, `302`) instead of `403`, Caddy is not
asking the app.

| Cause | Fix |
|-------|-----|
| The `jellyfin.example.com` site has no `forward_auth` block | Add it back exactly as in [Caddy and WireGuard](caddy-and-wireguard.md#caddyfile). |
| `forward_auth` has no `uri /check`, or a different path | Caddy then asks the app for the original path, which is not an auth answer. Use `uri /check`. |
| Caddy is still running the old config | `sudo docker compose exec -w /etc/caddy caddy caddy reload`, or `sudo docker compose restart caddy`. |
| An old host Caddy, or another proxy, is answering instead | `sudo ss -ltnp 'sport = :443'` should show only `docker-proxy`. Stop the other one. |
| Jellyfin is also reachable some other way (a published port, a second hostname, a port forward at home) | Remove it. Jellyfin should only be reachable through the tunnel from the VPS. |

## Jellyfin still locked after verifying

| Cause | Fix |
|-------|-----|
| The address unlocked is not the one the TV uses. The confirm page shows the address the portal saw. If the phone was on mobile data, a VPN, or iCloud Private Relay, that is not the household's address. | Verify again from a phone on home Wi-Fi with VPN and Private Relay off. Compare the address with `curl -4 https://ifconfig.me` from a computer on the same network. |
| Household public IP changed since verifying | Verify again. |
| Entry expired (`TYC_WHITELIST_TTL`, 72 hours by default) | Verify again. Re-verifying resets the timer. |
| The same person verified from another network since, which moves their unlock (the log shows `unlocked <new> for <email>, replacing <old>`) | Verify again from home. A household that needs two networks unlocked at once needs two trusted addresses. |
| The state file was deleted, the volume recreated, or the file was found corrupt and moved to `allowlist.json.corrupt` | Verify again. Check with `sudo docker compose exec takeyourcoat cat /data/allowlist.json`. |
| The check passes but Jellyfin does not answer (a `502` from Caddy) | The tunnel or Jellyfin is down. On the VPS: `sudo wg show` should show a recent handshake, and `sudo docker compose exec caddy wget -qO- http://10.0.0.2:8096/health` should answer. |

## IPv6 or private address message

The full message is: "This portal only works with public IPv4 addresses. Your
connection arrived over IPv6 or from a private network, which is not
supported." On Jellyfin it appears on the locked page (`403` from `/check`);
on the portal it appears on the link page.

| Cause | Fix |
|-------|-----|
| The hostname has an AAAA record and the client prefers IPv6. `/check` accepts IPv4 only. | Remove the AAAA records for both hostnames so clients connect over IPv4. |
| It happens for everyone | Proxy range, see [Everyone gets the locked page](#everyone-gets-the-locked-page). |
| You are testing from the VPS itself or from inside a private network | Test from a real household connection. |

## "That link is invalid or has expired."

Shown by both the link page and the Unlock button when the token is not in
memory.

| Cause | Fix |
|-------|-----|
| More than `TYC_TOKEN_TTL` (15 minutes by default) passed since the email was sent | Request a new link. |
| The link was already used: Unlock was pressed, then the page was reloaded or Unlock was pressed again | Nothing to fix if the first press succeeded. Otherwise request a new link. |
| An unlock attempt failed to save (`allowlist add` error) | The link is **not** used up by a failed save. Fix the error (see [State file permission denied](#state-file-permission-denied)) and press Unlock again, within the 15 minutes. |
| The container restarted after the email was sent (links are kept in memory only) | Request a new link. |
| The link was truncated when copied | Open it from the email directly. |

Opening the link without pressing Unlock does not use it up, so mail scanners
that prefetch links are not a cause.

## Mail never arrives

First look for a `send mail to` line:

```sh
sudo docker compose logs takeyourcoat | grep 'send mail to'
```

**No `send mail to` line at all.** The app either did not try, or the send
succeeded. By design it says nothing when:

- the address is not in `TYC_TRUSTED_EMAILS` (check spelling; case and spaces
  do not matter),
- that address already had `TYC_REQUESTS_PER_EMAIL_PER_HOUR` links (3 by
  default) from the same network in the last hour, or 12 from all networks,
  or
- the request came from an IPv6 or private address (a phone preferring IPv6,
  or the proxy range problem in
  [Everyone gets the locked page](#everyone-gets-the-locked-page)). No link is
  sent, because it could not be used from there anyway.

If none applies, the provider accepted the message: check spam, and for SES
check sandbox restrictions.

**A `send mail to alice@example.com: ...` line.** The error says why:

| Error contains | Cause | Fix |
|----------------|-------|-----|
| `i/o timeout` right after connecting, with port 465 | Port 465 uses implicit TLS, which is not supported | Use port 587 (`TYC_SMTP_PORT: "587"`, or remove the variable). |
| `i/o timeout` or `connection refused` on 587 | Outbound SMTP blocked by the VPS provider, or wrong host | Test with `nc -vz smtp.example.com 587` from the VPS. Some providers block outbound mail ports until you ask. |
| `STARTTLS` / `502` / `not supported` | Server does not offer STARTTLS on that port | Use the provider's submission port (587). The app will not send without TLS. |
| `x509:` or `certificate` | `TYC_SMTP_HOST` does not match the server certificate | Use the exact host name the provider documents, not an IP or alias. |
| `535` / `authentication failed` / `Username and Password not accepted` | Wrong credentials, or a normal password where an app password is required | Create an app password (Fastmail, Gmail) or use SES SMTP credentials. Update `.env` and recreate the container. |
| `554` / `not verified` / `rejected` | Sender not allowed by the provider | Make `TYC_SMTP_FROM` an address the account may send as (a verified identity for SES). |

After changing `.env`, recreate the container so it picks up the new value:

```sh
sudo docker compose up -d --force-recreate
```

## State file permission denied

The log shows `allowlist add <ip>: open /data/.allowlist-<random>: permission denied`
and the user sees "Something went wrong unlocking your network." The app runs
as UID 65532 and must be able to create files in `/data`. The link stays
valid, so once this is fixed the user can press Unlock again. If `/data` is
not even readable, the app does not start and logs `allowlist: ...`.

| Cause | Fix |
|-------|-----|
| `/data` is a bind mount to a host directory owned by root | `sudo chown 65532:65532 <host directory>`, or use the named volume from the shipped compose file. |
| The named volume was created by an older image or another container and is owned by root | Remove it and let the app recreate it: `sudo docker compose down`, `sudo docker volume rm <project>_tyc-data`, `sudo docker compose up -d`. Everyone verifies again. |
| A `user:` line in the compose file runs the app as a different UID | Remove it. The image already sets `USER 65532:65532`. |
| `read_only: true` and no volume on `/data` | Add the `tyc-data:/data` volume back. |

Check:

```sh
sudo docker compose exec takeyourcoat ls -ln /data
```

The directory and `allowlist.json` should be owned by `65532`.

## Caddy cannot get certificates

Caddy logs errors mentioning `acme`, `challenge` or `timeout`, and browsers
show a certificate warning.

| Cause | Fix |
|-------|-----|
| TCP 80 or 443 closed in the cloud firewall or host firewall | Open both (and UDP 443). See [VPS setup](vps-setup.md#2-open-the-ports). |
| A DNS A record is missing or points elsewhere | `dig +short A hello.example.com` and `dig +short A jellyfin.example.com` must print the VPS address. |
| Something else already listens on 80 or 443 (often the old host Caddy) | `sudo ss -ltnp 'sport = :80'`; stop it, then `sudo docker compose up -d`. |
| Too many attempts: Let's Encrypt rate limit | Wait, and keep the `caddy-data` volume so certificates are not re-requested on every recreate. |

## `curl` gets a 403 instead of a timeout

In v0.1 the proof that the gate worked was a `curl` to port 8920 that timed
out, because the kernel dropped the packets. In v0.2 the TLS connection
always succeeds and an unverified client gets a `403` with the locked page.
That is the expected result, not a fault. The locked page on the Jellyfin
hostname may look unstyled, because its stylesheet request is gated too.
