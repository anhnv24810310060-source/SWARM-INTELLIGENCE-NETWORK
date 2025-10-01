#!/usr/bin/env bash
set -euo pipefail
# Idempotent JetStream provisioning script.
# Requires: nats CLI (https://docs.nats.io/running-a-nats-service/nats_tools/nats_cli)
# Usage: NATS_URL=nats://127.0.0.1:4222 ./scripts/provision_jetstream.sh

NATS_URL=${NATS_URL:-nats://127.0.0.1:4222}

info() { echo "[JS] $*"; }
warn() { echo "[JS][WARN] $*" >&2; }

need() { command -v "$1" >/dev/null 2>&1 || { warn "missing dependency $1"; exit 1; }; }
need nats

# Streams definition (align with docs/jetstream-design.md)
# shellcheck disable=SC2016
create_stream() {
  local name=$1; shift
  local subjects=$1; shift
  local storage=$1; shift
  local max_age=$1; shift
  local replicas=$1; shift
  if nats --server "$NATS_URL" stream info "$name" >/dev/null 2>&1; then
    info "stream $name exists"
  else
    info "creating stream $name"
    nats --server "$NATS_URL" stream add "$name" --subjects="$subjects" --storage="$storage" --retention=limits --max-age="$max_age" --replicas="$replicas" --discard=old --dupe-window=2m --max-msgs=-1 --max-bytes=-1 --allow-rollup
  fi
}

create_stream INGEST_RAW_V1 'ingest.v1.raw' file 168h 3
create_stream INGEST_STATUS_V1 'ingest.v1.status' memory 24h 1
create_stream CONSENSUS_EVENTS_V1 'consensus.v1.*' file 720h 3

info "done"
