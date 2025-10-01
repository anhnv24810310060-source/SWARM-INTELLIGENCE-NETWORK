#!/usr/bin/env bash
set -euo pipefail
OUT_DOC="docs/perf-baseline.md"
TMP_JSON="target/criterion_summary.json"
TREND_CSV="docs/perf-trend.csv"
REG_JSON="target/perf-regressions.json"
PCT=${PERF_REGRESSION_PCT:-10}

mkdir -p target

echo "[perf] Running sensor-gateway benchmarks (may be skipped in CI without Rust nightly toolchain)." >&2
cargo bench -p sensor-gateway -- --noplot --save-baseline=auto > /tmp/bench.out 2>&1 || true

# Parse Criterion output lines that look like:
# raw_event_encode_256B ... time:   [123.45 ns 124.00 ns 124.70 ns]
python - <<'PY'
import re, pathlib, json, csv, datetime, statistics, sys
out = pathlib.Path('/tmp/bench.out').read_text(errors='ignore')
pattern = re.compile(r'^(raw_event_encode_[^\s]+).*?\[(.+?)\]$', re.MULTILINE)
results = {}
rows = []
ts = datetime.datetime.utcnow().replace(microsecond=0).isoformat() + 'Z'
for name, bracket in pattern.findall(out):
    # bracket form: '123.45 ns 124.00 ns 124.70 ns'
    parts = bracket.split()
    # Filter numeric + unit pairs
    nums = []
    i = 0
    while i < len(parts)-1:
        try:
            float(parts[i])
            unit = parts[i+1]
            nums.append((parts[i], unit))
            i += 2
        except ValueError:
            i += 1
    if len(nums) >= 3:  # min, mid, max
        (min_v, unit), (mid_v, _), (max_v, _) = nums[:3]
        rows.append([ts, name, min_v, mid_v, max_v, unit])
    results[name] = bracket

pathlib.Path('target').mkdir(exist_ok=True)
pathlib.Path('target/criterion_summary.json').write_text(json.dumps(results, indent=2))

# Append to trend CSV if we have rows
csv_path = pathlib.Path('docs/perf-trend.csv')
csv_path.parent.mkdir(exist_ok=True)
new_lines = []
if rows:
    if not csv_path.exists():
        new_lines.append(['timestamp','benchmark','min','mid','max','unit'])
    new_lines.extend(rows)
if new_lines:
    with csv_path.open('a', newline='') as f:
        w = csv.writer(f)
        for r in new_lines:
            w.writerow(r)
PY

if [[ -f "$TMP_JSON" ]]; then
  SUMMARY=$(cat "$TMP_JSON")
  if ! grep -q '<!-- PERF-AUTO-START -->' "$OUT_DOC"; then
    cat >> "$OUT_DOC" <<'EOF'

<!-- PERF-AUTO-START -->
<!-- PERF-AUTO-END -->
EOF
  fi
  awk -v data="$SUMMARY" 'BEGIN{printed=0}
    /<!-- PERF-AUTO-START -->/ {print; print "\nLatest Benchmark Summary (raw extract):\n"; print "```json"; print data; print "```"; skip=1; next}
    /<!-- PERF-AUTO-END -->/ {skip=0; print; next}
    skip!=1 {print}
  ' "$OUT_DOC" > "$OUT_DOC.tmp" && mv "$OUT_DOC.tmp" "$OUT_DOC"
  echo "[perf] Updated $OUT_DOC with latest benchmark summary and appended trend CSV (if data)." >&2
else
  echo "[perf] No summary JSON produced (possibly benches skipped)." >&2
fi

if [[ -f "$TREND_CSV" ]]; then
  echo "[perf] Trend rows now: $(wc -l < "$TREND_CSV") (includes header)." >&2
  # Trim CSV to last 6 months (approx 183 days)
  python - <<'PY'
import csv, datetime, pathlib, sys
path = pathlib.Path('docs/perf-trend.csv')
rows = list(csv.reader(path.open()))
if len(rows) <= 1:
    sys.exit(0)
header = rows[0]
idx_ts = header.index('timestamp') if 'timestamp' in header else 0
cutoff = datetime.datetime.utcnow() - datetime.timedelta(days=183)
kept = [header]
for r in rows[1:]:
    try:
        ts = datetime.datetime.fromisoformat(r[idx_ts].replace('Z',''))
    except Exception:
        continue
    if ts >= cutoff:
        kept.append(r)
if len(kept) != len(rows):
    tmp = path.with_suffix('.tmp')
    with tmp.open('w', newline='') as f:
        w = csv.writer(f)
        w.writerows(kept)
    tmp.replace(path)
    print(f"[perf] Trimmed CSV to last {len(kept)-1} rows (<=6 months)")
PY
  # Regression detection: emit JSON artifact with any regressions beyond threshold
  python - <<PY
import csv, datetime, statistics, pathlib, sys, json, os
threshold_pct = int(os.environ.get('PERF_REGRESSION_PCT','10'))
csv_path = pathlib.Path('docs/perf-trend.csv')
rows = list(csv.reader(csv_path.open()))
if len(rows) <= 1:
    pathlib.Path('target/perf-regressions.json').write_text('[]')
    sys.exit(0)
header = rows[0]
idx_ts = header.index('timestamp'); idx_bm = header.index('benchmark'); idx_mid = header.index('mid')
data = rows[1:]
now = datetime.datetime.utcnow()
by_bench = {}
for r in data:
    try:
        ts = datetime.datetime.fromisoformat(r[idx_ts].replace('Z',''))
    except Exception:
        continue
    if (now - ts).days > 10:
        continue
    by_bench.setdefault(r[idx_bm], []).append((ts, float(r[idx_mid])))

regressions = []
for bench, points in by_bench.items():
    points.sort(key=lambda x: x[0])
    current = points[-1][1]
    hist = [v for (t,v) in points[:-1] if (now - t).days <= 7]
    if len(hist) >= 3:
        median = statistics.median(hist)
        if median > 0 and current > median * (1 + threshold_pct/100):
            msg = f"{bench} midpoint {current:.2f} > {100+threshold_pct}% of 7d median {median:.2f}"
            print(f"[perf][REGRESSION][WARN] {msg}")
            regressions.append({
                'benchmark': bench,
                'current': current,
                'median_7d': median,
                'pct_change': (current/median - 1)*100,
                'threshold_pct': threshold_pct,
                'timestamp': now.isoformat()+'Z'
            })
pathlib.Path('target').mkdir(exist_ok=True)
pathlib.Path('target/perf-regressions.json').write_text(json.dumps(regressions, indent=2))
PY
  if [[ -f "$REG_JSON" ]]; then
    echo "[perf] Regression artifact: $REG_JSON (size $(wc -c < "$REG_JSON") bytes)" >&2
  fi
fi

