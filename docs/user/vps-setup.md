# VPS setup

[Operator guide](README.md) > VPS setup

Starting point: a fresh Debian 12 or Ubuntu 22.04/24.04 VPS with a public IPv4
address, SSH access as a user with `sudo`, and DNS A records for
`hello.example.com` and `jellyfin.example.com` pointing at the VPS. Replace the
placeholder names with your own throughout.

The app never creates the set or the firewall rule. It only runs `ipset add`.
Everything on this page is done once, by you, on the host.

For a single end-to-end walkthrough on Ubuntu, including notes for Oracle
Cloud, see [INSTALL.md](../../INSTALL.md). This page explains each step and
the pitfalls in more detail.

## 1. Install Docker

Docker's own apt repository (works for both Debian and Ubuntu):

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL "https://download.docker.com/linux/$(. /etc/os-release && echo "$ID")/gpg" -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$(. /etc/os-release && echo "$ID") $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

Check:

```sh
sudo docker compose version
```

## 2. Install ipset, iptables and the persistence packages

```sh
sudo apt-get install -y ipset iptables netfilter-persistent iptables-persistent ipset-persistent
```

`iptables-persistent` may ask whether to save the current rules. Either answer
is fine; you save again at the end of this page.

## 3. Install Caddy on the host

Install Caddy as a host package, not in Docker (see pitfall 1 below).

```sh
sudo apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt-get update
sudo apt-get install -y caddy
```

The Caddyfile itself is on [Caddy and WireGuard](caddy-and-wireguard.md).

## 4. Create the set

```sh
sudo ipset create jellyfin_clients hash:ip family inet timeout 259200
```

- `hash:ip family inet`: single IPv4 addresses. The app only ever adds public
  IPv4 addresses.
- `timeout 259200`: the set supports per-entry timeouts (default 72 hours). The
  app passes its own `timeout` on every add (from `TYC_WHITELIST_TTL`), so the
  set **must** be created with a `timeout` option or every add fails.
- The name must match `TYC_IPSET_NAME` (default `jellyfin_clients`).

## 5. Add the firewall rule

Drop traffic to the Jellyfin port from anything not in the set:

```sh
sudo iptables -I INPUT 1 -p tcp --dport 8920 -m set ! --match-set jellyfin_clients src -j DROP
```

This inserts at position 1 rather than appending (`-A`), which avoids pitfall 2.
The portal on 443 is not matched and stays open to everyone.

Block the Jellyfin port over IPv6 entirely (pitfall 3):

```sh
sudo ip6tables -I INPUT 1 -p tcp --dport 8920 -j DROP
```

If your VPS provider has a cloud firewall, allow inbound TCP 80, 443 and 8920,
and UDP 51820 for WireGuard. Port 80 is used by Caddy for certificates and
redirects.

## 6. The four ways the rule silently does nothing

Check each one after deploying.

1. **Caddy in Docker bypasses INPUT.** Traffic to a container's published port
   goes through the FORWARD and DOCKER chains, never INPUT. Run Caddy on the
   host (as above) or with `network_mode: host`. If Caddy must run in bridge
   mode, put the rule in the `DOCKER-USER` chain instead.
2. **Rule order.** An earlier ACCEPT for port 8920 (from ufw or an old setup)
   wins over a DROP further down. Confirm nothing accepts 8920 before your rule:

   ```sh
   sudo iptables -L INPUT -n --line-numbers
   ```

3. **IPv6 AAAA leak.** The set is IPv4 only. If `jellyfin.example.com` has an
   AAAA record, IPv6 clients connect over IPv6 and never meet the IPv4 rule.
   Publish no AAAA record for the Jellyfin hostname, and keep the `ip6tables`
   DROP above as a second line of defence. Check DNS (`dig` is in the
   `bind9-dnsutils` package):

   ```sh
   dig +short AAAA jellyfin.example.com
   ```

   The output should be empty.

4. **Prove it with curl.** From a network that has **not** verified (mobile
   data on a phone hotspot works), this must time out:

   ```sh
   curl -m 5 https://jellyfin.example.com:8920
   ```

   Then go through the portal from that network and run it again; it should
   now connect. You can also see the entry on the VPS:

   ```sh
   sudo ipset list jellyfin_clients
   ```

## 7. Persist across reboot

Neither the set nor the rule survives a reboot on its own. With the packages
from step 2, save both:

```sh
sudo netfilter-persistent save
```

This writes `/etc/iptables/ipsets`, `/etc/iptables/rules.v4` and
`/etc/iptables/rules.v6`. On boot, the `ipset-persistent` plugin restores the
set **before** the iptables rules are restored. That order matters: an
iptables rule that references a set that does not exist yet fails to load, and
the whole `rules.v4` restore fails with it.

Saving also saves the current set entries with their remaining timeouts. That
is harmless; they keep counting down after boot.

Check after the next reboot:

```sh
sudo ipset list -n
sudo iptables -L INPUT -n --line-numbers
```

Run `sudo netfilter-persistent save` again whenever you change the rule.

## Next

- [Caddy and WireGuard](caddy-and-wireguard.md)
- [Configuration](configuration.md)
- [Deployment](deployment.md)
