#!/usr/bin/env bash
set -euo pipefail

missing=0
header='SPDX-License-Identifier: Apache-2.0'

while IFS= read -r -d '' file; do
  if ! grep -q "$header" "$file"; then
    echo "[LICENSE] Missing header: $file"
    missing=$((missing+1))
  fi
done < <(find . -type f \( -name '*.rs' -o -name '*.go' -o -name '*.py' \) -print0)

if [ $missing -gt 0 ]; then
  echo "[LICENSE] $missing file(s) missing license header" >&2
  exit 1
fi

echo "[LICENSE] All checked files contain header"
