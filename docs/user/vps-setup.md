# VPS setup

[Operator guide](README.md) > VPS setup

Starting point: a fresh Debian 12 or Ubuntu 22.04/24.04 VPS with a public IPv4
address, SSH access as a user with `sudo`, and DNS A records for
`hello.example.com` and `app.example.com` pointing at the VPS. Replace the
placeholder names with your own throughout.

The host needs very little: Docker and three open ports. Caddy and the app
both run in the compose project. There is no ipset set, no iptables rule and
nothing to persist across reboots.

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

## 2. Open the ports

| Port | Why |
|------|-----|
| TCP 80 | Caddy: ACME certificate challenges and the HTTP to HTTPS redirect |
| TCP 443 | Caddy: both hostnames |
| UDP 443 | Caddy: HTTP/3 |
| UDP 51820 | WireGuard, see [Caddy and WireGuard](caddy-and-wireguard.md) |

Open them in your VPS provider's **cloud firewall** (security list, security
group) and in any **host firewall** such as ufw. Nothing else needs to be
reachable. The app itself publishes no port.

Nothing else may listen on 80 or 443. If you ran Caddy on the host for v0.1,
stop it (`sudo systemctl disable --now caddy`) before starting the compose
project.

## 3. Two ways it can go wrong

Check both after deploying.

1. **`forward_auth` misconfigured: the protected site reachable without verifying.**
   If the protected site in the Caddyfile has no `forward_auth` block, or it
   points somewhere other than `takeyourcoat:8080` with `uri /check`, Caddy
   proxies every request straight to the backend. Check the Caddyfile against
   [Caddy and WireGuard](caddy-and-wireguard.md#caddyfile), then prove it: from
   a network that has **not** verified (mobile data on a phone hotspot works),

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com
   ```

   must print `403`. Then go through the portal from that network and run it
   again; it should now print your backend's own status (`200` or a `302`
   redirect).

2. **Wrong proxy range: everyone gets the locked page, even after unlocking.**
   The app only believes `X-Forwarded-For` from addresses in
   `TYC_TRUSTED_PROXIES`. If that range does not cover the Caddy container's
   address, the app sees Caddy's own private address as the client, so every
   request looks like it comes from a private IP and `/check` answers `403`
   with "This portal only works with public IPv4 addresses". The emailed
   link page shows the same message, so nobody can unlock. The shipped
   compose file gives Caddy the fixed address `172.28.0.10` on the `web`
   network and trusts exactly that address. If you changed the subnet or
   Caddy's `ipv4_address`, change `TYC_TRUSTED_PROXIES` to match.
   See [Configuration](configuration.md#trusted-proxies).

## Next

- [Caddy and WireGuard](caddy-and-wireguard.md)
- [Configuration](configuration.md)
- [Deployment](deployment.md)
