#!/usr/bin/env bash
set -euo pipefail
# Minimal E2E detection pipeline smoke test.
# Goal: Validate ingest -> detection -> alert publish path works (design section 4.1 + 5.1 readiness).

NATS_URL=${NATS_URL:-"nats://127.0.0.1:4222"}
BIN=${BIN:-"./services/sensor-gateway/target/release/sensor-gateway"}
TIMEOUT=${TIMEOUT:-25}
TMP_LOG=$(mktemp)

cleanup() { kill "$PID" 2>/dev/null || true; rm -f "$TMP_LOG"; }
trap cleanup EXIT

echo "[E2E] SwarmGuard detection smoke test starting" >&2
if ! command -v nats >/dev/null 2>&1; then
  echo "[E2E][WARN] nats CLI not installed -> skipping test (treat as pass)." >&2
  exit 0
fi

if [[ ! -x "$BIN" ]]; then
  echo "[E2E] building sensor-gateway (release)" >&2
  (cd services/sensor-gateway && cargo build --release >/dev/null 2>&1)
fi

echo "[E2E] launching sensor-gateway" >&2
"$BIN" >"$TMP_LOG" 2>&1 &
PID=$!
sleep 3

MSG='{"event_type":"process_create","command":"suspicious.exe","severity":"high"}'
echo "[E2E] publishing synthetic event" >&2
echo "$MSG" | nats pub ingest.v1.raw --server "$NATS_URL" >/dev/null

echo "[E2E] waiting for alert signal..." >&2
FOUND=0
for i in $(seq 1 "$TIMEOUT"); do
  if grep -qi 'threat.v1.alert' "$TMP_LOG"; then
    FOUND=1; break
  fi
  sleep 1
done

if [[ $FOUND -eq 1 ]]; then
  echo "[E2E] PASS alert observed" >&2
  exit 0
else
  echo "[E2E] FAIL alert not observed in ${TIMEOUT}s" >&2
  echo "--- LOG SNIPPET ---" >&2
  tail -n 100 "$TMP_LOG" | sed -e 's/^/[LOG] /' >&2
  exit 1
fi