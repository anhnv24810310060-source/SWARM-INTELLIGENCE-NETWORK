#!/usr/bin/env bash
set -euo pipefail
# Basic CPU or memory stress using yes or tail allocation.
MODE=${MODE:-cpu} # cpu|mem
DURATION=${DURATION:-30}
case "$MODE" in
  cpu)
    echo "Stressing CPU for $DURATION s";
    timeout "$DURATION" bash -c 'while :; do :; done' || true ;;
  mem)
    SIZE_MB=${SIZE_MB:-256}
    echo "Allocating ${SIZE_MB}MB for $DURATION s";
    timeout "$DURATION" python3 - <<EOF
buf=['x'*1024*1024 for _ in range(${SIZE_MB})]
import time; time.sleep(${DURATION})
EOF
    ;;
  *) echo "Unknown MODE"; exit 1;;
 esac
