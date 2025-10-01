#!/usr/bin/env bash
set -euo pipefail
# Randomly kill a percentage of processes matching pattern.
PATTERN=${PATTERN:-consensus-core}
PCT=${PCT:-30}
PIDS=( $(pgrep -f "$PATTERN" || true) )
COUNT=${#PIDS[@]}
if [ "$COUNT" -eq 0 ]; then echo "No processes found"; exit 0; fi
KILL_NUM=$(( COUNT * PCT / 100 ))
if [ "$KILL_NUM" -lt 1 ]; then KILL_NUM=1; fi
shuf -e "${PIDS[@]}" | head -n "$KILL_NUM" | while read -r pid; do
  echo "Killing $pid"; kill -9 "$pid" || true;
 done
