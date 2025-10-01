#!/usr/bin/env bash
set -euo pipefail
# Simple E2E detection alert flow test
# Preconditions: running NATS server on 127.0.0.1:4222
# Steps:
# 1. Start sensor-gateway with detection enabled & rules file
# 2. Subscribe to threat.v1.alert.detected
# 3. Inject line containing rule R001 pattern
# 4. Assert alert received within timeout

NATS_URL=${NATS_URL:-127.0.0.1:4222}
BIN=${BIN:-target/debug/sensor-gateway}
RULES=${RULES:-configs/detection-rules.yaml}
TIMEOUT=${TIMEOUT:-10}
TMP_OUT=$(mktemp)

# Launch NATS if not responding (best-effort local)
if ! nc -z 127.0.0.1 4222 2>/dev/null; then
  echo "[info] starting ephemeral nats-server" >&2
  (nats-server -p 4222 >/dev/null 2>&1 &) || true
  sleep 1
fi

# Subscribe in background
(nats sub threat.v1.alert.detected --count=1 >"$TMP_OUT" 2>/dev/null &)
SUB_PID=$!

# Run sensor-gateway one-shot injection mode
LINE="synthetic-event-42 marker"
INGEST_FILE=$(mktemp)
echo "$LINE" > "$INGEST_FILE"
SWARM_RUN_ONCE=1 DETECTION_RULES_PATH="$RULES" INGEST_FILE="$INGEST_FILE" NATS_URL="$NATS_URL" "$BIN" >/dev/null 2>&1 || {
  echo "[error] sensor-gateway failed"; exit 1; }

# Wait for alert
ELAPSED=0
while [[ ! -s "$TMP_OUT" && $ELAPSED -lt $TIMEOUT ]]; do
  sleep 1; ELAPSED=$((ELAPSED+1))
done

if [[ ! -s "$TMP_OUT" ]]; then
  echo "[fail] No alert received within ${TIMEOUT}s"; exit 2
fi

# Basic validation: check rule id presence
if ! grep -q 'R001' "$TMP_OUT"; then
  echo "[fail] Alert payload missing expected rule id"; exit 3
fi

echo "[pass] Detection alert flow succeeded in ${ELAPSED}s"
exit 0
