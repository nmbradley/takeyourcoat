# Caddy and WireGuard

[Operator guide](README.md) > Caddy and WireGuard

## Port layout

Both hostnames share port 443 on the VPS's one public IPv4 address. Caddy
tells them apart by name, which a port-based firewall rule never could.

| Hostname and port | Served by | Proxies to | Gated |
|-------------------|-----------|------------|-------|
| `hello.example.com:443` | Caddy | app at `takeyourcoat:8080` | No, open to everyone |
| `jellyfin.example.com:443` | Caddy | Jellyfin at `10.0.0.2:8096` over WireGuard | Yes, by `forward_auth` to the app's `/check` |
| `:80` | Caddy | HTTP to HTTPS redirect, ACME challenges | No |
| `takeyourcoat:8080` | takeyourcoat | none | Only on the compose network; no published port |

Jellyfin clients (Apple TV, Swiftfin, Infuse, the web UI) use
`https://jellyfin.example.com` as the server address, with no port.

## Caddyfile

The repository ships a `Caddyfile` next to `docker-compose.yml`. The compose
file mounts it read-only into the Caddy container at `/etc/caddy/Caddyfile`.

```caddyfile
# Portal: open to everyone, proxied to the app.
hello.example.com {
	reverse_proxy takeyourcoat:8080
}

# Jellyfin: each request is first checked with the app; only unlocked IPs reach the backend.
jellyfin.example.com {
	forward_auth takeyourcoat:8080 {
		uri /check
	}
	reverse_proxy 10.0.0.2:8096
}
```

Edit three things: the two hostnames, and `10.0.0.2:8096`, which is the
address Caddy uses to reach Jellyfin (your home peer's WireGuard address and
Jellyfin's port). Leave `takeyourcoat:8080` alone; it is the app's service
name on the compose network. After editing, reload:

```sh
cd /opt/takeyourcoat
sudo docker compose exec -w /etc/caddy caddy caddy reload
```

If an editor replaced the file rather than rewriting it, the container still
sees the old copy; `sudo docker compose restart caddy` picks up the new one.

How the gate works:

- For every request to `jellyfin.example.com`, `forward_auth` first sends
  `GET /check` to the app, with the real client address in `X-Forwarded-For`.
- `200` from the app: Caddy continues to `reverse_proxy` and Jellyfin gets the
  request. Anything else: Caddy returns the app's response (a `403` and the
  "This network is not unlocked" page) to the client, and Jellyfin sees
  nothing.
- The check is a map lookup in memory, so running it on every request,
  including video segments, costs next to nothing.

Why the client address is right:

- Caddy connects to the app from its fixed address on the compose network,
  `172.28.0.10`, which the shipped compose file sets as
  `TYC_TRUSTED_PROXIES`. The app therefore reads the client address from the
  last hop of `X-Forwarded-For`, which Caddy sets to the real client IP. See
  [Configuration](configuration.md#trusted-proxies).
- Docker publishes 80 and 443 with NAT rules that keep the client's source
  address, so Caddy sees the real client. If the VPS has IPv6 and a client
  arrives over it, the app sees an IPv6 or private address (Docker relays such
  connections from the network gateway) and answers with the locked page,
  which is the intended behaviour for an IPv4-only portal.
- Caddy obtains certificates for both names over ports 80 and 443, so both DNS
  names must point at the VPS and both ports must be open.

Keep the `forward_auth` block. Without it Jellyfin is open to everyone; see
[VPS setup](vps-setup.md#3-two-ways-it-can-go-wrong).

Avoid `log` blocks on the portal site, or keep them short-lived: the magic
link's token is in the `GET /verify?token=...` query string and would be
written to Caddy's access log. See [Security model](security-model.md).

## WireGuard sketch

Pick a tunnel range that does not overlap the VPS's own network. Oracle Cloud
VCNs default to `10.0.0.0/16` with the gateway at `10.0.0.1`, so a tunnel on
`10.0.0.0/24` would hijack the route to the gateway and drop your SSH session
when `wg0` comes up. Check with `ip route` before choosing. The examples here
use `10.0.0.x`; substitute something unused such as `10.66.0.x` if it clashes.

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

The Caddy container reaches `10.0.0.2` through the host: its traffic leaves
the compose network, is routed into `wg0`, and Docker rewrites its source to
the host's tunnel address. Nothing extra is needed on the VPS for that.

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

Through the tunnel every viewer reaches Jellyfin from one address: the Caddy
container's traffic as it arrives over WireGuard, normally the VPS tunnel
address `10.0.0.1`. Without further setup Jellyfin sees one client for
everybody, so its failed-login lockout and its logs cannot tell viewers apart.

Find the address Jellyfin sees connections from: check its logs (Dashboard >
Logs) after loading a page through `https://jellyfin.example.com`. In
Jellyfin, go to **Dashboard > Networking**, add that address to **Known
proxies**, save, and restart Jellyfin. Jellyfin then takes the client address
from the `X-Forwarded-For` header that Caddy sends.

Only list that one address. Listing anything broader lets other hosts forge
their client address to Jellyfin.
