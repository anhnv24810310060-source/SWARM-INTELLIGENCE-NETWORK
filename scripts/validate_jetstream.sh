#!/usr/bin/env bash
set -euo pipefail
# Validate live JetStream configuration against spec file (best-effort).
# Non-fatal: always exits 0, emitting WARN lines on drift.
# Requirements: nats CLI, jq.

SPEC=${SPEC:-infra/jetstream-spec.yaml}
NATS_URL=${NATS_URL:-nats://127.0.0.1:4222}

info() { echo "[JS-VALIDATE] $*"; }
warn() { echo "[JS-VALIDATE][WARN] $*" >&2; }

need() { command -v "$1" >/dev/null 2>&1 || { warn "missing dependency $1"; exit 0; }; }
need nats
need jq
# yq optional (improves parsing when present)
if command -v yq >/dev/null 2>&1; then
  HAVE_YQ=1
else
  HAVE_YQ=0
fi

if [[ ! -f "$SPEC" ]]; then
  warn "spec file $SPEC missing"; exit 0
fi

# Quick connectivity check
if ! nats --server "$NATS_URL" account info >/dev/null 2>&1; then
  warn "NATS unreachable at $NATS_URL - skipping validation"
  exit 0
fi

current_streams=$(nats --server "$NATS_URL" stream list --json 2>/dev/null | jq -r '.[]?.config.name' || true)

declare -A SPEC_STORAGE SPEC_REPLICAS SPEC_MAXAGE SPEC_SUBJECTS SPEC_RETENTION SPEC_DISCARD SPEC_DUPE
declare -A SPEC_CONS_DELIVER SPEC_CONS_ACK

if [[ $HAVE_YQ -eq 1 ]]; then
  info "Parsing spec with yq"
  # yq extracts arrays; we join subjects with commas for easy compare later.
  mapfile -t names < <(yq '.streams[].name' "$SPEC" 2>/dev/null || true)
  for n in "${names[@]}"; do
    storage=$(yq ".streams[] | select(.name==\"$n\").storage" "$SPEC")
    replicas=$(yq ".streams[] | select(.name==\"$n\").replicas" "$SPEC")
    max_age=$(yq ".streams[] | select(.name==\"$n\").max_age" "$SPEC")
    subj_join=$(yq -o=json ".streams[] | select(.name==\"$n\").subjects" "$SPEC" | jq -r 'join(",")')
  retention=$(yq ".streams[] | select(.name==\"$n\").retention" "$SPEC")
  discard=$(yq ".streams[] | select(.name==\"$n\").discard" "$SPEC")
  dupe=$(yq ".streams[] | select(.name==\"$n\").dupe_window" "$SPEC")
  SPEC_STORAGE[$n]=$storage
    SPEC_REPLICAS[$n]=$replicas
    SPEC_MAXAGE[$n]=$max_age
    SPEC_SUBJECTS[$n]=$subj_join
  SPEC_RETENTION[$n]=$retention
  SPEC_DISCARD[$n]=$discard
  SPEC_DUPE[$n]=$dupe
  done
    # Consumers
    cons_count=$(yq ".streams[] | select(.name==\"$n\").consumers | length" "$SPEC" 2>/dev/null || echo 0)
    if [[ "$cons_count" =~ ^[0-9]+$ && $cons_count -gt 0 ]]; then
      for ((i=0;i<cons_count;i++)); do
        cname=$(yq ".streams[] | select(.name==\"$n\").consumers[$i].name" "$SPEC")
        cdeliver=$(yq ".streams[] | select(.name==\"$n\").consumers[$i].deliver_policy" "$SPEC")
        cack=$(yq ".streams[] | select(.name==\"$n\").consumers[$i].ack_policy" "$SPEC")
        SPEC_CONS_DELIVER["$n/$cname"]=$cdeliver
        SPEC_CONS_ACK["$n/$cname"]=$cack
      done
    fi
else
  info "Parsing spec with fallback shell parser (install yq for richer validation)"
  current_name=""
  while IFS= read -r line; do
    case "$line" in
      "  - name:"*) current_name=$(echo "$line" | sed -E 's/.*name: *//');;
      "    storage:"*) [[ -n "$current_name" ]] && SPEC_STORAGE[$current_name]=$(echo "$line"|sed -E 's/.*storage: *//');;
      "    replicas:"*) [[ -n "$current_name" ]] && SPEC_REPLICAS[$current_name]=$(echo "$line"|sed -E 's/.*replicas: *//');;
      "    max_age:"*) [[ -n "$current_name" ]] && SPEC_MAXAGE[$current_name]=$(echo "$line"|sed -E 's/.*max_age: *//');;
      "    subjects:"*) [[ -n "$current_name" ]] && SPEC_SUBJECTS[$current_name]=$(echo "$line"|sed -E 's/.*subjects: *\[(.*)\].*/\1/' | tr -d '"');;
  "    retention:"*) [[ -n "$current_name" ]] && SPEC_RETENTION[$current_name]=$(echo "$line"|sed -E 's/.*retention: *//');;
  "    discard:"*) [[ -n "$current_name" ]] && SPEC_DISCARD[$current_name]=$(echo "$line"|sed -E 's/.*discard: *//');;
  "    dupe_window:"*) [[ -n "$current_name" ]] && SPEC_DUPE[$current_name]=$(echo "$line"|sed -E 's/.*dupe_window: *//');;
      "    description:"*) : ;; # ignore
    esac
  done < "$SPEC"
fi

rc=0
for name in "${!SPEC_STORAGE[@]}"; do
  if ! echo "$current_streams" | grep -q "^$name$"; then
    warn "missing stream $name (not found in live cluster)"
    continue
  fi
  info "Checking $name"
  json=$(nats --server "$NATS_URL" stream info "$name" --json 2>/dev/null || true)
  if [[ -z "$json" ]]; then
    warn "no json for $name"; continue
  fi
  live_storage=$(echo "$json" | jq -r '.config.storage // empty' | tr '[:upper:]' '[:lower:]')
  live_replicas=$(echo "$json" | jq -r '.config.num_replicas // empty')
  live_max_age=$(echo "$json" | jq -r '.config.max_age // empty')
  # subjects array joined by comma for basic comparison
  live_subjects=$(echo "$json" | jq -r '.config.subjects | join(",")')
  want_storage=${SPEC_STORAGE[$name]}
  want_repl=${SPEC_REPLICAS[$name]}
  want_max_age=${SPEC_MAXAGE[$name]:-}
  want_subjects_raw=${SPEC_SUBJECTS[$name]:-}
  want_retention=${SPEC_RETENTION[$name]:-}
  want_discard=${SPEC_DISCARD[$name]:-}
  want_dupe=${SPEC_DUPE[$name]:-}
  # Normalize want subjects: remove spaces
  want_subjects=$(echo "$want_subjects_raw" | tr -d ' ')
  if [[ "$live_storage" != "$want_storage" ]]; then
    warn "storage mismatch $name: live=$live_storage expected=$want_storage"
  fi
  if [[ "$live_replicas" != "$want_repl" ]]; then
    warn "replicas mismatch $name: live=$live_replicas expected=$want_repl"
  fi
  if [[ -n "$want_max_age" && "$live_max_age" != "" ]]; then
    # Convert want_max_age (e.g., 168h) to hours -> seconds for comparison: JetStream reports nanoseconds in max_age (duration) sometimes; check form
    # We will only warn if pattern ends with h and live_max_age numeric (nanoseconds) diverges >5%.
    if [[ "$want_max_age" =~ ^([0-9]+)h$ && "$live_max_age" =~ ^[0-9]+$ ]]; then
      want_hours=${BASH_REMATCH[1]}
      # JetStream max_age is in nanoseconds; hours -> ns
      want_ns=$((want_hours * 3600 * 1000000000))
      diff=$(( live_max_age > want_ns ? live_max_age - want_ns : want_ns - live_max_age ))
      # >5% difference
      if (( diff * 100 > want_ns * 5 )); then
        warn "max_age drift $name: live=${live_max_age}ns expected~=${want_ns}ns (diff >5%)"
      fi
    fi
  fi
  if [[ -n "$want_subjects" ]]; then
    # Sort both lists for stable compare
    IFS=',' read -r -a arr_live <<< "$live_subjects"
    IFS=',' read -r -a arr_want <<< "$want_subjects"
    sorted_live=$(printf '%s\n' "${arr_live[@]}" | sort | tr '\n' ',')
    sorted_want=$(printf '%s\n' "${arr_want[@]}" | sort | tr '\n' ',')
    if [[ "$sorted_live" != "$sorted_want" ]]; then
      warn "subjects mismatch $name: live=[$live_subjects] expected=[$want_subjects]"
    fi
  fi
  # Additional fields (best-effort; only warn if live present)
  live_retention=$(echo "$json" | jq -r '.config.retention // empty' | tr '[:upper:]' '[:lower:]')
  if [[ -n "$want_retention" && -n "$live_retention" && "$live_retention" != "$want_retention" ]]; then
    warn "retention mismatch $name: live=$live_retention expected=$want_retention"
  fi
  live_discard=$(echo "$json" | jq -r '.config.discard // empty' | tr '[:upper:]' '[:lower:]')
  if [[ -n "$want_discard" && -n "$live_discard" && "$live_discard" != "$want_discard" ]]; then
    warn "discard mismatch $name: live=$live_discard expected=$want_discard"
  fi
  live_dupe=$(echo "$json" | jq -r '.config.duplicate_window // empty')
  if [[ -n "$want_dupe" && -n "$live_dupe" && "$live_dupe" != "$want_dupe" ]]; then
    warn "dupe_window mismatch $name: live=$live_dupe expected=$want_dupe"
  fi
done

# Validate consumers deliver_policy / ack_policy (best-effort)
if [[ $HAVE_YQ -eq 1 ]]; then
  for key in "${!SPEC_CONS_DELIVER[@]}"; do
    stream=${key%%/*}
    cname=${key##*/}
    # Get consumer info (if existing) via nats CLI (no direct consumer list json by deliver policy, need to attempt info)
    # If consumer absent -> warn
    if ! nats --server "$NATS_URL" consumer info "$stream" "$cname" --json >/dev/null 2>&1; then
      warn "consumer missing $stream/$cname"
      continue
    fi
    cjson=$(nats --server "$NATS_URL" consumer info "$stream" "$cname" --json 2>/dev/null || echo '')
    [[ -z "$cjson" ]] && { warn "no consumer json $stream/$cname"; continue; }
    live_deliver=$(echo "$cjson" | jq -r '.config.deliver_policy // empty' | tr '[:upper:]' '[:lower:]')
    live_ack=$(echo "$cjson" | jq -r '.config.ack_policy // empty' | tr '[:upper:]' '[:lower:]')
    want_deliver=$(echo "${SPEC_CONS_DELIVER[$key]}" | tr '[:upper:]' '[:lower:]')
    want_ack=$(echo "${SPEC_CONS_ACK[$key]}" | tr '[:upper:]' '[:lower:]')
    if [[ -n "$want_deliver" && -n "$live_deliver" && "$want_deliver" != "$live_deliver" ]]; then
      warn "consumer deliver_policy mismatch $stream/$cname: live=$live_deliver expected=$want_deliver"
    fi
    if [[ -n "$want_ack" && -n "$live_ack" && "$want_ack" != "$live_ack" ]]; then
      warn "consumer ack_policy mismatch $stream/$cname: live=$live_ack expected=$want_ack"
    fi
  done
fi

info "validation complete"
exit 0
