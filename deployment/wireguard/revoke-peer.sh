#!/usr/bin/env bash
# Run as root on the WireGuard server: revoke-peer.sh DEVICE_NAME
set -euo pipefail

readonly CONFIG=/etc/wireguard/wg0.conf
readonly CLIENT_DIR=/etc/wireguard/clients

name=${1:-}
if [[ ! $name =~ ^[A-Za-z0-9_-]+$ ]] || [[ $EUID -ne 0 ]] || [[ ! -f "$CLIENT_DIR/$name.pub" ]]; then
  echo "usage: $0 EXISTING_DEVICE_NAME" >&2
  exit 64
fi

public_key=$(cat "$CLIENT_DIR/$name.pub")
wg set wg0 peer "$public_key" remove
sed -i "/# managed-peer: $name$/,/^AllowedIPs = 10\.77\.0\..*\/32$/d" "$CONFIG"
rm -f "$CLIENT_DIR/$name.conf" "$CLIENT_DIR/$name.pub"
echo "revoked $name"
