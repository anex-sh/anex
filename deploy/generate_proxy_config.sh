#!/bin/bash

set -euo pipefail

print_usage() {
  cat <<'USAGE'
Usage: generate_proxy_config.sh \
  --gateway-endpoint HOST:PORT \
  --pod-limit NUM \
  --dns IP \
  --allowed-ips CIDR[,CIDR...] \
  [--proxy-conf-out PATH] \
  [--wg-conf-out PATH] \
  [--iface IFACE]

Description:
  Generates two artifacts using the same freshly generated keys:
   - Proxy config YAML written to --proxy-conf-out (default: ./proxy-conf.yaml).
   - WireGuard server config file written to --wg-conf-out (default: ./wg0.conf).

Arguments:
  --gateway-endpoint  Public endpoint of the gateway in form HOST:PORT (required)
  --pod-limit         Number of peers to generate (1..128) (required)
  --dns               DNS server IP for the server section (required)
  --allowed-ips       Comma-separated list of CIDRs for server.allowed_ips (required)
  --proxy-conf-out    Path to write the proxy config YAML (default: proxy-conf.yaml)
  --wg-conf-out       Path to write the WireGuard server config (default: wg0.conf)
  --iface             Egress interface used in iptables MASQUERADE rules (default: enX0)

Examples:
  ./generate_proxy_config.sh \
    --gateway-endpoint 3.79.237.153:51820 \
    --pod-limit 3 \
    --dns 10.100.0.10 \
    --allowed-ips 10.0.0.0/16,10.100.0.10/32 \
    --proxy-conf-out wireguard-keys.yaml \
    --wg-conf-out wg0.conf
USAGE
}


# Generate a WireGuard keypair using OpenSSL (no wg dependency)
gen_wg_keys() {
  local pem priv pub

  # Generate ephemeral PEM keypair
  pem=$(openssl genpkey -algorithm X25519)

  # Extract 32-byte private scalar
  priv=$(
    printf '%s' "$pem" \
    | openssl pkey -outform DER \
    | tail -c 32 \
    | base64
  )

  # Extract 32-byte public key
  pub=$(
    printf '%s' "$pem" \
    | openssl pkey -pubout -outform DER \
    | tail -c 32 \
    | base64
  )

  # Export to caller’s scope
  WG_PRIV=$priv
  WG_PUB=$pub
}


# Defaults / placeholders
GATEWAY_ENDPOINT=""
POD_LIMIT=""
DNS_IP=""
ALLOWED_IPS=""
PROXY_CONF_OUT="proxy-config.yaml"
WG_CONF_OUT="wg0.conf"
IFACE="enX0"

# Parse named arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --gateway-endpoint)
      GATEWAY_ENDPOINT=${2:-}
      shift 2
      ;;
    --pod-limit)
      POD_LIMIT=${2:-}
      shift 2
      ;;
    --dns)
      DNS_IP=${2:-}
      shift 2
      ;;
    --allowed-ips)
      ALLOWED_IPS=${2:-}
      shift 2
      ;;
    --proxy-conf-out)
      PROXY_CONF_OUT=${2:-}
      shift 2
      ;;
    --wg-conf-out)
      WG_CONF_OUT=${2:-}
      shift 2
      ;;
    --iface)
      IFACE=${2:-}
      shift 2
      ;;
    -h|--help)
      print_usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      print_usage >&2
      exit 2
      ;;
  esac
done

# Validate required args
err=0
if [[ -z "$GATEWAY_ENDPOINT" ]]; then echo "Error: --gateway-endpoint is required" >&2; err=1; fi
if [[ -z "$POD_LIMIT" ]]; then echo "Error: --pod-limit is required" >&2; err=1; fi
if [[ -z "$DNS_IP" ]]; then echo "Error: --dns is required" >&2; err=1; fi
if [[ -z "$ALLOWED_IPS" ]]; then echo "Error: --allowed-ips is required" >&2; err=1; fi

if [[ $err -ne 0 ]]; then
  echo >&2
  print_usage >&2
  exit 2
fi

# Validate pod limit is an integer between 1 and 128
if ! [[ "$POD_LIMIT" =~ ^[0-9]+$ ]]; then
  echo "Error: --pod-limit must be an integer" >&2
  exit 2
fi
if (( POD_LIMIT < 1 || POD_LIMIT > 128 )); then
  echo "Error: --pod-limit must be between 1 and 128 (inclusive)" >&2
  exit 2
fi

# Helper to emit YAML with proper indentation
indent() { sed "s/^/  /"; }

# Generate server keys
gen_wg_keys
SERVER_PRIV="$WG_PRIV"
SERVER_PUB="$WG_PUB"

# Begin building YAML content in a variable, then write to file
YAML_OUT="server:
  private_key: $SERVER_PRIV
  public_key: $SERVER_PUB
  dns: $DNS_IP
  endpoint: $GATEWAY_ENDPOINT
  allowed_ips: \"10.254.254.1/32,$ALLOWED_IPS\"
peers:
"

# Generate peers and collect info for wg0.conf
# Address scheme mirrors example: start from 10.254.254.11/32 incrementing last octet
# gateway_port_offset starts from 10000 and increments by 100
start_oct4=11
port_offset=10000
step=100

# Arrays to store peers
peer_addrs=()
peer_pubs=()

for ((i=0; i<POD_LIMIT; i++)); do
  gen_wg_keys
  peer_priv="$WG_PRIV"
  peer_pub="$WG_PUB"

  oct4=$(( start_oct4 + i ))
  address="10.254.254.$oct4/32"
  offset=$(( port_offset + i * step ))

  peer_addrs+=("$address")
  peer_pubs+=("$peer_pub")

  YAML_OUT+="  - address: $address
    private_key: $peer_priv
    public_key: $peer_pub
    gateway_port_offset: $offset
"

done

# Write YAML to proxy-conf-out first
printf "%s" "$YAML_OUT" > "$PROXY_CONF_OUT"

# Then write wg server config using the same keys
# Extract port from endpoint (HOST:PORT)
WG_PORT=${GATEWAY_ENDPOINT##*:}

# Build iptables PostUp/PreDown rules based on allowed IPs (excluding the interface address 10.254.254.1/32)
# Ensure 10.254.254.0/24 as source subnet
SRC_SUBNET="10.254.254.0/24"

# Filter allowed IPs list to omit 10.254.254.1/32 if present
ALLOWED_LIST=${ALLOWED_IPS// /}

postup_rules=()
predown_rules=()
IFS=',' read -r -a allowed_array <<< "$ALLOWED_LIST"
for cidr in "${allowed_array[@]}"; do
  [[ -z "$cidr" ]] && continue
  if [[ "$cidr" == "10.254.254.1/32" ]]; then
    continue
  fi
  postup_rules+=("PostUp = iptables -t nat -A POSTROUTING -s $SRC_SUBNET -d $cidr -o $IFACE -j MASQUERADE")
  predown_rules+=("PreDown = iptables -t nat -D POSTROUTING -s $SRC_SUBNET -d $cidr -o $IFACE -j MASQUERADE")
done

{
  echo "[Interface]"
  echo "Address = 10.254.254.1/24"
  echo "ListenPort = $WG_PORT"
  echo "PrivateKey = $SERVER_PRIV"
  echo "SaveConfig = false"
  echo
  echo "# Allow forwarding and NAT into VPC + (optional) EKS Service CIDR"
  echo "PostUp = iptables -A FORWARD -i %i -j ACCEPT"
  echo "PostUp = iptables -A FORWARD -o %i -j ACCEPT"
  for r in "${postup_rules[@]}"; do echo "$r"; done
  echo
  echo "PreDown = iptables -D FORWARD -i %i -j ACCEPT"
  echo "PreDown = iptables -D FORWARD -o %i -j ACCEPT"
  for r in "${predown_rules[@]}"; do echo "$r"; done
  echo
  # Peers
  for idx in "${!peer_pubs[@]}"; do
    num=$((idx+1))
    printf "\n# Peer %02d\n" "$num"
    echo "[Peer]"
    echo "PublicKey       = ${peer_pubs[$idx]}"
    echo "AllowedIPs      = ${peer_addrs[$idx]}"
    echo "PersistentKeepalive = 25"
  done
} > "$WG_CONF_OUT"
