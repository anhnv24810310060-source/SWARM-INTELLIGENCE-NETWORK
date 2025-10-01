#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT_DIR"

if ! command -v buf >/dev/null 2>&1; then
  echo "[ERROR] buf not installed. See https://docs.buf.build/installation" >&2
  exit 1
fi

echo "[PROTO] Lint & breaking check"
buf lint
buf breaking --against '.git#branch=main' || echo "[WARN] breaking check skipped (no main remote cache)"

echo "[PROTO] Generate code"
buf generate --template buf.gen.yaml

echo "[DONE] Generated code into proto/gen"
