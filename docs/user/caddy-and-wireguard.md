# Caddy and WireGuard

[Operator guide](README.md) > Caddy and WireGuard

## Port layout

The firewall rule matches on destination port, so the portal and Jellyfin need
different ports on the same public IP.

| Hostname and port | Served by | Proxies to | Gated by ipset |
|-------------------|-----------|------------|----------------|
| `hello.example.com:443` | Caddy | app on `127.0.0.1:8080` | No, open to everyone |
| `jellyfin.example.com:8920` | Caddy | Jellyfin at `10.0.0.2:8096` over WireGuard | Yes |
| `:80` | Caddy | HTTP to HTTPS redirect, ACME challenges | No |
| `127.0.0.1:8080` | takeyourcoat | none | Loopback only, not reachable from outside |

Jellyfin clients (Apple TV, Swiftfin, Infuse, the web UI) use
`https://jellyfin.example.com:8920` as the server address.

## Caddyfile

`/etc/caddy/Caddyfile`:

```caddyfile
hello.example.com {
    reverse_proxy 127.0.0.1:8080
}

jellyfin.example.com:8920 {
    reverse_proxy 10.0.0.2:8096
}
```

Replace `10.0.0.2` with your home peer's WireGuard address. Reload:

```sh
sudo systemctl reload caddy
```

Why this works with the app's defaults:

- Caddy connects to the app from `127.0.0.1`, which is in the default
  `TYC_TRUSTED_PROXIES` (`127.0.0.1,::1`). The app therefore reads the client
  address from the last hop of `X-Forwarded-For`, which Caddy sets to the real
  client IP. See [Configuration](configuration.md).
- Caddy obtains certificates for both names over ports 80 and 443, even though
  the Jellyfin site listens on 8920, so both DNS names must point at the VPS.
- Caddy runs on the host, so its traffic on 8920 passes through the INPUT chain
  where the ipset rule lives. See [VPS setup](vps-setup.md#6-the-four-ways-the-rule-silently-does-nothing).

Avoid `log` blocks on the portal site, or keep them short-lived: the magic
link's token is in the `GET /verify?token=...` query string and would be
written to Caddy's access log. See [Security model](security-model.md).

## WireGuard sketch

The home server dials out to the VPS, so the home router needs no port
forwarding. Addresses here are placeholders: VPS `10.0.0.1`, home `10.0.0.2`.

Install and generate keys on both machines:

```sh
sudo apt-get install -y wireguard
wg genkey | sudo tee /etc/wireguard/private.key | wg pubkey | sudo tee /etc/wireguard/public.key
sudo chmod 600 /etc/wireguard/private.key
```

VPS `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.0.0.1/24
ListenPort = 51820
PrivateKey = <contents of the VPS private.key>

[Peer]
# home server
PublicKey = <contents of the home public.key>
AllowedIPs = 10.0.0.2/32
```

Home `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.0.0.2/24
PrivateKey = <contents of the home private.key>

[Peer]
# VPS
PublicKey = <contents of the VPS public.key>
Endpoint = <VPS public IPv4>:51820
AllowedIPs = 10.0.0.1/32
PersistentKeepalive = 25
```

Bring it up on both and check the handshake:

```sh
sudo systemctl enable --now wg-quick@wg0
sudo wg show
```

Open UDP 51820 inbound on the VPS (and in any provider firewall).

### Home firewall

The home server should accept Jellyfin traffic on the tunnel only from the
VPS tunnel address, and nothing else from the tunnel:

```sh
sudo iptables -A INPUT -i wg0 -s 10.0.0.1 -p tcp --dport 8096 -j ACCEPT
sudo iptables -A INPUT -i wg0 -j DROP
sudo netfilter-persistent save
```

If the VPS is ever compromised, this limits what it can reach at home to the
Jellyfin port.

## Jellyfin "Known proxies"

Through the tunnel every viewer reaches Jellyfin from the VPS tunnel address
`10.0.0.1`. Without further setup Jellyfin sees one client for everybody, so
its failed-login lockout and its logs cannot tell viewers apart.

In Jellyfin, go to **Dashboard > Networking**, add `10.0.0.1` to **Known
proxies**, save, and restart Jellyfin. Jellyfin then takes the client address
from the `X-Forwarded-For` header that Caddy sends.

Only list the VPS tunnel address. Listing anything broader lets other hosts
forge their client address to Jellyfin.
