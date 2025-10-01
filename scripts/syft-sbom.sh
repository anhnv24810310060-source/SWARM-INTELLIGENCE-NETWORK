#!/usr/bin/env bash
set -euo pipefail
if ! command -v syft >/dev/null 2>&1; then
  echo "[ERROR] syft not installed (https://github.com/anchore/syft)" >&2
  exit 1
fi
IMG="$1"
OUT="${2:-sbom-$(echo "$IMG" | tr '/:' '_').json}"
syft "$IMG" -o json > "$OUT"
echo "[SBOM] written $OUT"
