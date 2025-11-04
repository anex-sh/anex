#!/usr/bin/env bash
set -euo pipefail

# --- Config ---
NAT_CHAIN="WG_NAT"
FWD_CHAIN="WG_FWD"

SRC_CIDR="10.0.0.0/16"
# Base settings for clients
BASE_DEST_PREFIX="10.254.254"   # clients start at 10.254.254.11
BASE_DEST_OCTET=11               # first client IP = .11
BASE_PORT_START=10000            # first client's port range starts here
PORTS_PER_CLIENT=100             # each client reserves 100 ports

IPT=${IPT:-iptables}     # override with IPT=iptables-legacy if you need
SUDO=${SUDO:-}           # set to "sudo" if you’re not root

# --- Helpers ---
rule_exists() {
  # $1: table, $2: builtin chain, remaining: rule spec
  local table="$1" ; shift
  local builtin="$1" ; shift
  $SUDO $IPT -t "$table" -C "$builtin" "$@" >/dev/null 2>&1
}

del_jump_if_exists() {
  # $1: table, $2: builtin chain, $3: target chain
  local table="$1" builtin="$2" target="$3"
  if rule_exists "$table" "$builtin" -j "$target"; then
    $SUDO $IPT -t "$table" -D "$builtin" -j "$target"
  fi
}

drop_chain_if_exists() {
  # $1: table, $2: chain
  local table="$1" chain="$2"

  # If chain exists, flush + delete (needs any jumps removed first)
  if $SUDO $IPT -t "$table" -S "$chain" >/dev/null 2>&1; then
    $SUDO $IPT -t "$table" -F "$chain" || true
    $SUDO $IPT -t "$table" -X "$chain" || true
  fi
}

ensure_fresh_chain() {
  # $1: table, $2: builtin chain to hook from, $3: our chain name
  local table="$1" builtin="$2" chain="$3"

  # Remove any prior jump from builtin → our chain
  del_jump_if_exists "$table" "$builtin" "$chain"
  # Drop any stale chain with the same name
  drop_chain_if_exists "$table" "$chain"
  # Create new empty chain
  $SUDO $IPT -t "$table" -N "$chain"
  # Hook builtin → our chain (append at end; use -I to put at top if you prefer)
  $SUDO $IPT -t "$table" -A "$builtin" -j "$chain"
}

apply_rules() {
  local pod_limit="$1"
  # Make sure we start clean and controlled
   ensure_fresh_chain nat PREROUTING "$NAT_CHAIN"
   ensure_fresh_chain filter FORWARD "$FWD_CHAIN"

  # Loop through all clients and install DNAT + FORWARD rules
  for ((i=0; i<pod_limit; i++)); do
    local oct4=$(( BASE_DEST_OCTET + i ))
    local dest_ip="${BASE_DEST_PREFIX}.${oct4}"

    local port_start=$(( BASE_PORT_START + i * PORTS_PER_CLIENT ))
    local port_end=$(( port_start + PORTS_PER_CLIENT - 1 ))
    local dports="${port_start}:${port_end}"

    # --- NAT PREROUTING rules (DNAT) ---
    $SUDO $IPT -t nat -A "$NAT_CHAIN" \
      -p tcp -s "$SRC_CIDR" --dport "$dports" \
      -j DNAT --to-destination "$dest_ip"

    # --- FORWARD rules (allow forwarded traffic to dest) ---
    $SUDO $IPT -t filter -A "$FWD_CHAIN" \
      -p tcp -d "$dest_ip" --dport "$dports" \
      -j ACCEPT
  done
}

reset_rules() {
  # Remove builtin hooks, then drop our chains
  del_jump_if_exists nat PREROUTING "$NAT_CHAIN"
  del_jump_if_exists filter FORWARD "$FWD_CHAIN"

  drop_chain_if_exists nat "$NAT_CHAIN"
  drop_chain_if_exists filter "$FWD_CHAIN"
}

usage() { 
  cat <<EOF
Usage: $0 apply --pod-limit NUM | reset

  apply  - drops any existing ${NAT_CHAIN}/${FWD_CHAIN}, recreates them, and installs rules
           generates DNAT/FORWARD rules for NUM clients (1..128), each reserving ${PORTS_PER_CLIENT} ports
  reset  - removes hooks from PREROUTING/FORWARD and deletes ${NAT_CHAIN}/${FWD_CHAIN}

Flags:
  --pod-limit NUM  Number of clients to generate routing for (required for apply)

Env:
  IPT  - iptables binary (default: iptables). Set to iptables-legacy if needed.
  SUDO - set to "sudo" if you are not root.
EOF
}

main() {
  local cmd="${1:-}"
  shift || true

  case "$cmd" in
    apply)
      local POD_LIMIT=""
      while [[ $# -gt 0 ]]; do
        case "$1" in
          --pod-limit)
            POD_LIMIT=${2:-}
            shift 2
            ;;
          -h|--help)
            usage; exit 0 ;;
          *)
            echo "Unknown argument for apply: $1" >&2
            usage; exit 1 ;;
        esac
      done
      if [[ -z "$POD_LIMIT" ]]; then
        echo "Error: --pod-limit is required for apply" >&2
        usage; exit 1
      fi
      if ! [[ "$POD_LIMIT" =~ ^[0-9]+$ ]]; then
        echo "Error: --pod-limit must be an integer" >&2; exit 1
      fi
      if (( POD_LIMIT < 1 || POD_LIMIT > 128 )); then
        echo "Error: --pod-limit must be between 1 and 128" >&2; exit 1
      fi
      apply_rules "$POD_LIMIT"
      ;;
    reset)
      reset_rules ;;
    *)
      usage ; exit 1 ;;
  esac
}
main "$@"
