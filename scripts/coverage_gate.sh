#!/usr/bin/env bash
set -euo pipefail
MIN=${COVERAGE_MIN:-70}
# Per-language specific floors (overridable by env)
RUST_MIN=${RUST_COVERAGE_MIN:-70}
GO_MIN=${GO_COVERAGE_MIN:-60}
PY_MIN=${PY_COVERAGE_MIN:-50}

red() { echo -e "\e[31m$1\e[0m"; }
green() { echo -e "\e[32m$1\e[0m"; }

echo "[COVERAGE] Aggregate minimum: ${MIN}% | rust>=${RUST_MIN} go>=${GO_MIN} python>=${PY_MIN}"

TOTAL=0; COUNT=0

# Rust (tarpaulin) XML: look for line-rate attribute or lines-covered/lines-valid
if [[ -f tarpaulin-report.xml ]]; then
  RUST_PCT=$(python - <<'PY'
import xml.etree.ElementTree as ET
try:
  t=ET.parse('tarpaulin-report.xml').getroot()
  # fallback: compute from lines-covered/lines-valid if present
  lines_covered=0; lines_valid=0
  for pkg in t.findall('.//package'):
    for cls in pkg.findall('.//class'):
      lines=cls.find('lines')
      if lines is None: continue
      for line in lines.findall('line'):
        lines_valid+=1
        if line.get('hits','0')!='0': lines_covered+=1
  pct = 0.0 if lines_valid==0 else (lines_covered/lines_valid*100)
  print(f"{pct:.2f}")
except Exception:
  print('0')
PY
  )
  echo "[COVERAGE][rust] ${RUST_PCT}% (min ${RUST_MIN}%)"
  TOTAL=$(python - <<PY
rust=${RUST_PCT}
print(rust)
PY
  ); COUNT=$((COUNT+1))
fi

# Go coverage: coverage.out format; use go tool cover -func
if [[ -f go-coverage.txt ]]; then
  GOPCT=$(grep 'total:' go-coverage.txt | awk '{print $3}' | sed 's/%//') || true
  [[ -n "${GOPCT}" ]] && echo "[COVERAGE][go] ${GOPCT}% (min ${GO_MIN}%)" && TOTAL=$(python - <<PY
rust=${TOTAL};g=${GOPCT or 0};c=${COUNT};print(rust+g)
PY
) && COUNT=$((COUNT+1))
fi

# Python coverage xml
if [[ -f coverage-python.xml ]]; then
  PYPCT=$(python - <<'PY'
import xml.etree.ElementTree as ET
try:
  r=ET.parse('coverage-python.xml').getroot()
  pct=float(r.get('line-rate','0'))*100
  print(f"{pct:.2f}")
except Exception:
  print('0')
PY
  )
  echo "[COVERAGE][python] ${PYPCT}% (min ${PY_MIN}%)"
  TOTAL=$(python - <<PY
t=${TOTAL};p=${PYPCT};print(t+p)
PY
); COUNT=$((COUNT+1))
fi

if [[ $COUNT -gt 0 ]]; then
  AVG=$(python - <<PY
import sys
print(f"{(${TOTAL})/${COUNT}:.2f}")
PY
)
  echo "[COVERAGE] Average=${AVG}% (components=${COUNT})"
  PASS=$(python - <<PY
avg=${AVG};min=${MIN}
print('yes' if float(avg) >= float(min) else 'no')
PY
)
  FAIL=0
  # Individual gates
  if [[ -n "${RUST_PCT:-}" ]]; then awk -v a="${RUST_PCT}" -v m="${RUST_MIN}" 'BEGIN{exit !(a+0 < m+0)}' && { red "[COVERAGE] rust FAIL (<${RUST_MIN}%)"; FAIL=1; } || true; fi
  if [[ -n "${GOPCT:-}" ]]; then awk -v a="${GOPCT}" -v m="${GO_MIN}" 'BEGIN{exit !(a+0 < m+0)}' && { red "[COVERAGE] go FAIL (<${GO_MIN}%)"; FAIL=1; } || true; fi
  if [[ -n "${PYPCT:-}" ]]; then awk -v a="${PYPCT}" -v m="${PY_MIN}" 'BEGIN{exit !(a+0 < m+0)}' && { red "[COVERAGE] python FAIL (<${PY_MIN}%)"; FAIL=1; } || true; fi

  if [[ $PASS == 'no' ]]; then red "[COVERAGE] aggregate FAIL (<${MIN}%)"; FAIL=1; fi
  if [[ $FAIL -eq 1 ]]; then exit 1; fi
  green "[COVERAGE] PASS all thresholds"
else
  red "[COVERAGE] No coverage artifacts found"
  exit 1
fi
