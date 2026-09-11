#!/usr/bin/env bash
# Run as root on the WireGuard server: add-peer.sh DEVICE_NAME 10.77.0.10
set -euo pipefail

readonly CONFIG=/etc/wireguard/wg0.conf
readonly CLIENT_DIR=/etc/wireguard/clients
readonly ENDPOINT=td1.tekomi.vn:51820
readonly PBX_IP=103.72.57.182

name=${1:-}
address=${2:-}
if [[ ! $name =~ ^[A-Za-z0-9_-]+$ ]] || [[ ! $address =~ ^10\.77\.0\.(1[0-9]|2[0-4][0-9]|25[0-4])$ ]]; then
  echo "usage: $0 DEVICE_NAME 10.77.0.10" >&2
  exit 64
fi
if [[ $EUID -ne 0 ]] || [[ ! -f $CONFIG ]]; then
  echo "run as root after wg0 is configured" >&2
  exit 1
fi
if [[ -e "$CLIENT_DIR/$name.conf" ]] || grep -Eq "^[[:space:]]*AllowedIPs = $address/32$" "$CONFIG"; then
  echo "device name or VPN address already exists" >&2
  exit 1
fi

umask 077
install -d -m 700 "$CLIENT_DIR"
client_private=$(wg genkey)
client_public=$(printf '%s' "$client_private" | wg pubkey)
server_public=$(cat /etc/wireguard/server.pub)

printf '\n# managed-peer: %s\n[Peer]\nPublicKey = %s\nAllowedIPs = %s/32\n' \
  "$name" "$client_public" "$address" >> "$CONFIG"
wg set wg0 peer "$client_public" allowed-ips "$address/32"
printf '%s\n' "$client_public" > "$CLIENT_DIR/$name.pub"
printf '[Interface]\nAddress = %s/32\nPrivateKey = %s\n\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s/32\nPersistentKeepalive = 25\n' \
  "$address" "$client_private" "$server_public" "$ENDPOINT" "$PBX_IP" > "$CLIENT_DIR/$name.conf"
chmod 600 "$CLIENT_DIR/$name.conf" "$CLIENT_DIR/$name.pub"
echo "created $CLIENT_DIR/$name.conf"
