#!/usr/bin/env bash
set -euo pipefail
# Inject network latency / loss using tc netem. Requires sudo.
IFACE=${IFACE:-eth0}
ACTION=${1:-add}
case "$ACTION" in
  add)
    sudo tc qdisc add dev "$IFACE" root netem delay ${DELAY_MS:-200}ms loss ${LOSS_PCT:-10}% || true ;;
  change)
    sudo tc qdisc change dev "$IFACE" root netem delay ${DELAY_MS:-200}ms loss ${LOSS_PCT:-10}% || true ;;
  clear|del|remove)
    sudo tc qdisc del dev "$IFACE" root || true ;;
  *) echo "Usage: $0 {add|change|clear}"; exit 1;;
 esac
