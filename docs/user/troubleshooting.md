# Troubleshooting

[Operator guide](README.md) > Troubleshooting

Start with the log:

```sh
cd /opt/takeyourcoat
sudo docker compose logs --tail 100 takeyourcoat
```

## Quick reference

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Portal works and says "Done", but Jellyfin is still blocked for everyone | The allow side of the rule never matches: entry added to a different set, wrong address added, or the tunnel/Caddy path is down | [Details](#jellyfin-still-blocked-after-verifying) |
| Jellyfin reachable from networks that never verified | Traffic never meets the DROP rule: Caddy in Docker, an earlier ACCEPT, or IPv6 via an AAAA record | [Details](#jellyfin-reachable-without-verifying) |
| "That link is invalid or has expired." | Link older than `TYC_TOKEN_TTL`, already used, or the app restarted | [Details](#that-link-is-invalid-or-has-expired) |
| "This portal only works with public IPv4 addresses..." | Client came in over IPv6, or the app sees Caddy's address instead of the client's | [Details](#ipv6-or-private-address-error-page) |
| Mail never arrives | Silent by design for unknown or rate-limited addresses; otherwise SMTP errors in the log | [Details](#mail-never-arrives) |
| "Something went wrong unlocking your network" and `ipset add` errors in the log | `NET_ADMIN` missing (compose file edited), set missing, or set created without `timeout` | [Details](#ipset-add-permission-denied) |
| Everything worked until a reboot, now Jellyfin is open or `ipset add` fails | Set or rule not persisted, or restored in the wrong order | [Details](#set-disappears-after-reboot) |
| Container exits immediately | Config validation failed | Read the `config:` line and see [Configuration](configuration.md#validation) |

## Jellyfin still blocked after verifying

Check what is in the set and what the rule references:

```sh
sudo ipset list jellyfin_clients
sudo iptables -L INPUT -n -v --line-numbers
```

| Cause | Fix |
|-------|-----|
| `TYC_IPSET_NAME` differs from the set named in the iptables rule | Make them match, then verify again. |
| The address added is not the one the TV uses. The confirm page shows the address the portal saw. If the phone was on mobile data, a VPN, or iCloud Private Relay, that is not the household's address. | Verify again from a phone on home Wi-Fi with VPN and Private Relay off. Compare the address with `curl -4 https://ifconfig.me` from a computer on the same network. |
| Household public IP changed since verifying | Verify again. |
| Entry expired (`TYC_WHITELIST_TTL`, 72 hours by default) | Verify again. Re-verifying resets the timer. |
| Tunnel or Caddy is down, so even allowed clients time out | On the VPS: `sudo wg show` should show a recent handshake, and `curl -m 5 http://10.0.0.2:8096/health` should answer. Check `sudo systemctl status caddy`. |

## Jellyfin reachable without verifying

Test from a network that has not verified:

```sh
curl -m 5 https://jellyfin.example.com:8920
```

If it connects, one of these is true:

| Cause | How to tell | Fix |
|-------|-------------|-----|
| Caddy runs in Docker with a published port, so traffic goes through FORWARD/DOCKER, not INPUT | `sudo docker ps` shows a Caddy container with `0.0.0.0:8920->` | Run Caddy on the host or with `network_mode: host`, or put the rule in `DOCKER-USER`. |
| An earlier rule accepts 8920 (ufw, an old setup) | `sudo iptables -L INPUT -n --line-numbers` shows an ACCEPT for 8920 above the DROP | Delete it, or re-insert the DROP with `-I INPUT 1`. |
| Client connected over IPv6 | `dig +short AAAA jellyfin.example.com` returns an address | Remove the AAAA record, and add `sudo ip6tables -I INPUT 1 -p tcp --dport 8920 -j DROP`. |
| Rule was lost on reboot | The DROP rule is missing from the listing | See [Set disappears after reboot](#set-disappears-after-reboot). |

Full background: [VPS setup](vps-setup.md#6-the-four-ways-the-rule-silently-does-nothing).

## "That link is invalid or has expired."

Shown by both the link page and the Unlock button when the token is not in
memory.

| Cause | Fix |
|-------|-----|
| More than `TYC_TOKEN_TTL` (15 minutes by default) passed since the email was sent | Request a new link. |
| The link was already used: Unlock was pressed, then the page was reloaded or Unlock was pressed again | Nothing to fix if the first press succeeded. Otherwise request a new link. |
| An unlock attempt failed on the host side (`ipset add` error). The token is used up at that point. | Fix the `ipset add` error, then request a new link. |
| The container restarted after the email was sent (links are kept in memory only) | Request a new link. |
| The link was truncated when copied | Open it from the email directly. |

Opening the link without pressing Unlock does not use it up, so mail scanners
that prefetch links are not a cause.

## IPv6 or private address error page

The full message is: "This portal only works with public IPv4 addresses. Your
connection arrived over IPv6 or from a private network, which is not
supported."

| Cause | Fix |
|-------|-----|
| `hello.example.com` has an AAAA record and the phone prefers IPv6 | Remove the AAAA record for the portal hostname so browsers connect over IPv4. |
| Caddy connects from an address not in `TYC_TRUSTED_PROXIES`, so the app sees Caddy (a loopback or Docker bridge address) as the client | Run Caddy on the host with the app on host networking (Caddy then connects from `127.0.0.1`), or add Caddy's source address to `TYC_TRUSTED_PROXIES`. |
| `TYC_TRUSTED_PROXIES` was set to an empty value | Remove the variable to get the default `127.0.0.1,::1`. |
| You are testing from the VPS itself or from inside a private network that reaches the portal directly | Test from a real household connection. |

The address check happens before the token is touched, so the link still works
once the cause is fixed.

## Mail never arrives

First look for a `send mail to` line:

```sh
sudo docker compose logs takeyourcoat | grep 'send mail to'
```

**No `send mail to` line at all.** The app either did not try, or the send
succeeded. By design it says nothing when:

- the address is not in `TYC_TRUSTED_EMAILS` (check spelling; case and spaces
  do not matter), or
- that address already had `TYC_REQUESTS_PER_EMAIL_PER_HOUR` links (3 by
  default) in the last hour.

If neither applies, the provider accepted the message: check spam, and for SES
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

## `ipset add` permission denied

The log shows `ipset add <ip>: ...` and the user sees "Something went wrong
unlocking your network."

### Why it happens

The shipped setup already runs the app as root inside the container with
exactly one capability, `NET_ADMIN`: the image has no `USER` line, and the
compose file drops every other capability and sets `no-new-privileges`. ipset
inherits `NET_ADMIN` from the app when it is executed. If ipset still reports
a permission error (typically ending in
`Kernel error received: Operation not permitted`), something has changed that
setup. Check the service in your `docker-compose.yml`:

| Check | Why it matters |
|-------|----------------|
| `cap_add: [NET_ADMIN]` is present | With `cap_drop: [ALL]` and no `cap_add`, root in the container has no capabilities at all. |
| `network_mode: host` is present | Without it, ipset works on the container's own network namespace, not the host's, so the host set is never touched (you usually see `The set with the given name does not exist`). |
| There is **no** `user:` line | A non-root user starts with no capabilities, and `no-new-privileges` stops any file capability from granting them on exec. Remove the line. |
| The image is the published one or built from the repository Dockerfile | A custom image with a `USER` line fails the same way as a `user:` line. |
| The Docker daemon does not use `userns-remap` | With user namespace remapping, capabilities do not apply to the host network namespace. Add `userns_mode: host` to the service. |

Check on your host:

```sh
cd /opt/takeyourcoat
sudo docker compose exec takeyourcoat grep -E '^(Uid|CapEff|CapBnd|NoNewPrivs)' /proc/self/status
sudo docker compose exec takeyourcoat ipset list -n
```

A correct container shows `Uid` of `0`, `CapEff: 0000000000001000` and
`CapBnd: 0000000000001000` (`NET_ADMIN` only), and `NoNewPrivs: 1`. The second
command then prints the host's set names, including `jellyfin_clients`.

After fixing the compose file, recreate the container and repeat the check:

```sh
sudo docker compose up -d --force-recreate
```

### Other `ipset add` errors

| Error contains | Fix |
|----------------|-----|
| `The set with the given name does not exist` | Create the set ([VPS setup](vps-setup.md#4-create-the-set)) or fix `TYC_IPSET_NAME`. |
| a complaint about `timeout` | The set was created without `timeout`. Destroy and recreate it with `timeout 259200`, then save. |
| `executable file not found` | You are running a custom image without `ipset` on `PATH`. Use the published image or the repository Dockerfile. |

## Set disappears after reboot

| Cause | Fix |
|-------|-----|
| Never saved | `sudo netfilter-persistent save` after creating the set and rule. |
| `ipset-persistent` not installed, so the set is not saved or restored | `sudo apt-get install -y ipset-persistent`, recreate the set, save again. |
| The set is restored after the iptables rules, so `rules.v4` fails to load (Jellyfin is then open to everyone) | Use `ipset-persistent` with `netfilter-persistent`, which restores sets first. If you restore by hand, run `ipset restore` before `iptables-restore`. |

Check after boot:

```sh
sudo ipset list -n
sudo iptables -L INPUT -n --line-numbers
sudo systemctl status netfilter-persistent
```
