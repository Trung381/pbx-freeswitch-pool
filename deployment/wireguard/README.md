# WireGuard access for Zoiper

This deployment keeps SIP private while retaining the existing OneConnect
trunk. The server's WireGuard configuration and every private key live only on
the server under `/etc/wireguard`; do not commit them.

## Network plan

| Item | Value |
| --- | --- |
| WireGuard server | `10.77.0.1/24` on `wg0` |
| Client addresses | one `/32` per device, starting at `10.77.0.10` |
| Public VPN transport | UDP `51820` |
| Zoiper route | `103.72.57.182/32` through WireGuard (split tunnel) |
| FreeSWITCH RTP | UDP `16384:16399` through `wg0` |

`pbx-sip-firewall` permits SIP/RTP only from `wg0` and configured carrier
CIDRs, then drops the same traffic arriving from the public interface.

## Server bootstrap

Run from the checked-out repository on the PBX host:

```bash
sudo apt update
sudo apt install -y wireguard wireguard-tools
sudo install -d -m 700 /etc/wireguard /etc/pbx-firewall
sudo umask 077
sudo sh -c 'wg genkey | tee /etc/wireguard/server.key | wg pubkey > /etc/wireguard/server.pub'
sudo install -m 600 deployment/wireguard/wg0.conf.example /etc/wireguard/wg0.conf
sudo install -m 600 deployment/firewall/sip-allowlist.env.example /etc/pbx-firewall/sip-allowlist.env
sudo install -m 755 deployment/firewall/pbx-sip-firewall /usr/local/sbin/pbx-sip-firewall
sudo install -m 644 deployment/firewall/pbx-sip-firewall.service /etc/systemd/system/pbx-sip-firewall.service
```

Replace the placeholder `PrivateKey` in `/etc/wireguard/wg0.conf` with the
contents of `/etc/wireguard/server.key`. Then start in this order:

```bash
sudo systemctl enable --now wg-quick@wg0
sudo systemctl daemon-reload
sudo systemctl enable --now pbx-sip-firewall
```

## Add one device

Create a per-device profile on the PBX host; it creates the client private key
with mode `0600` and adds the matching public key to `wg0` without a restart:

```bash
sudo deployment/wireguard/add-peer.sh zoiper-1001 10.77.0.10
```

Import the generated `/etc/wireguard/clients/zoiper-1001.conf` into the
WireGuard client. Its shape is:

```ini
[Interface]
Address = 10.77.0.10/32
PrivateKey = CLIENT_PRIVATE_KEY
DNS = 1.1.1.1

[Peer]
PublicKey = SERVER_PUBLIC_KEY
Endpoint = td1.tekomi.vn:51820
AllowedIPs = 103.72.57.182/32
PersistentKeepalive = 25
```

In Zoiper, retain the existing PBX host and credentials; import and activate
WireGuard first. Revoke a lost device immediately with
`sudo deployment/wireguard/revoke-peer.sh zoiper-1001`.

## Verification and rollback

```bash
sudo wg show
sudo systemctl status wg-quick@wg0 pbx-sip-firewall
sudo iptables -L DOCKER-USER -n -v --line-numbers
```

To roll back the VPN access without changing the carrier route, run
`sudo systemctl disable --now wg-quick@wg0`. Do not stop
`pbx-sip-firewall`: it continues to protect public SIP and the OneConnect
trunk.
