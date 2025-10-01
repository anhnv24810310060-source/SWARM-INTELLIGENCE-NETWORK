#!/usr/bin/env bash
set -euo pipefail
# Generate simple sparkline SVG(s) from docs/perf-trend.csv using gnuplot if available.
# Produces: docs/perf-sparkline.svg (mid values of first benchmark) and per-benchmark variants.

CSV="docs/perf-trend.csv"
OUT_GLOBAL="docs/perf-sparkline.svg"
LIMIT=${PERF_SPARKLINE_POINTS:-120}

if ! command -v gnuplot >/dev/null 2>&1; then
  echo "[sparkline][WARN] gnuplot not installed; skipping." >&2
  exit 0
fi

if [[ ! -f "$CSV" ]]; then
  echo "[sparkline][WARN] missing $CSV" >&2
  exit 0
fi

# Extract benchmark list & time series (mid) to temp files.
benchmarks=$(awk -F',' 'NR>1 {print $2}' "$CSV" | sort -u)

# Aggregate encode_1KB category if present (merge all with substring encode_1KB)
ENCODE1KB_AGG_FILE="/tmp/encode_1kb_agg.dat"
grep 'encode_1KB' "$CSV" >/dev/null 2>&1 || true
if grep -q 'encode_1KB' "$CSV"; then
  # Collect all encode_1KB rows (timestamp, benchmark, mid)
  awk -F',' 'NR==1 {next} /encode_1KB/ {print NR-1","$2","$4}' "$CSV" > /tmp/encode_1kb_all.tmp || true
  # Build per-benchmark mid series preserving relative order (line index acts as ordinal time)
  # Then average across benchmarks for each ordinal position.
  # We'll pivot: ordinal -> list of mids -> average.
  python - <<'PY'
import pathlib, statistics
lines = pathlib.Path('/tmp/encode_1kb_all.tmp').read_text().strip().splitlines()
by_bench = {}
max_len = 0
for ln in lines:
  try:
    ordinal_str, bench, mid = ln.split(',')
    ordinal = int(ordinal_str)
    by_bench.setdefault(bench, []).append((ordinal, float(mid)))
  except ValueError:
    continue
for bench, series in by_bench.items():
  series.sort(key=lambda x: x[0])
  if len(series) > max_len:
    max_len = len(series)

# We align by positional index within each series (not absolute ordinal) because different benchmarks may start later.
rows = []
for i in range(max_len):
  vals = []
  for series in by_bench.values():
    if i < len(series):
      vals.append(series[i][1])
  if vals:
    rows.append((i, sum(vals)/len(vals)))

with open('/tmp/encode_1kb_agg.dat','w') as f:
  for idx, avg in rows[-120:]:  # LIMIT fallback (mirrors bash LIMIT default 120)
    f.write(f"{idx},{avg}\n")
PY
fi

gen_plot() {
  local bench="$1" out="$2" max_points="$3"
  awk -F',' -v b="$bench" 'NR>1 && $2==b {print NR-1 "," $4}' "$CSV" | tail -n "$max_points" > /tmp/series.dat
  if [[ ! -s /tmp/series.dat ]]; then
    echo "[sparkline] no data for $bench" >&2; return
  fi
  gnuplot <<GNUPLOT
set terminal svg size 220,40 dynamic enhanced fname 'Verdana' fsize 9
set output '$out'
unset key
unset border
unset tics
set margins 0,0,0,0
set style line 1 lc rgb '#007acc' lt 1 lw 1
plot '/tmp/series.dat' using 1:2 with lines ls 1
GNUPLOT
}

# Generate per first benchmark as global badge.
first_bench=$(echo "$benchmarks" | head -n1)
if [[ -n "$first_bench" ]]; then
  gen_plot "$first_bench" "$OUT_GLOBAL" "$LIMIT"
fi

# Optionally generate per-benchmark badges (named docs/perf-<bench>.svg)
for b in $benchmarks; do
  safe=$(echo "$b" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-')
  gen_plot "$b" "docs/perf-${safe}.svg" "$LIMIT" || true
done

if [[ -s "$ENCODE1KB_AGG_FILE" ]]; then
  gnuplot <<GNUPLOT
set terminal svg size 220,40 dynamic enhanced fname 'Verdana' fsize 9
set output 'docs/perf-sparkline-encode_1KB.svg'
unset key; unset border; unset tics; set margins 0,0,0,0
set style line 1 lc rgb '#cc5500' lt 1 lw 1
plot '$ENCODE1KB_AGG_FILE' using 1:2 with lines ls 1
GNUPLOT
  echo "[sparkline] generated encode_1KB aggregate badge docs/perf-sparkline-encode_1KB.svg" >&2
fi

echo "[sparkline] generated SVG badges (first benchmark => $OUT_GLOBAL)." >&2
