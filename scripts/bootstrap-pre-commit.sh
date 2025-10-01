#!/usr/bin/env bash
set -euo pipefail

if ! command -v pre-commit >/dev/null 2>&1; then
  echo "[bootstrap] pre-commit not found, attempting install via pipx/pip" >&2
  if command -v pipx >/dev/null 2>&1; then
    pipx install pre-commit || true
  else
    pip install --user pre-commit || true
  fi
fi

if ! command -v pre-commit >/dev/null 2>&1; then
  echo "[bootstrap] Failed to install pre-commit. Please install manually." >&2
  exit 1
fi

pre-commit install --install-hooks || pre-commit install

echo "[bootstrap] Running initial hooks (may take a while)" >&2
pre-commit run --all-files || true

echo "[bootstrap] Completed." >&2
