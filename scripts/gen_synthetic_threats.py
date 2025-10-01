#!/usr/bin/env python3
"""Generate synthetic benign and malicious events for detection benchmarking.
Output: newline-delimited payloads (mixed) with labels file optional.
"""
import argparse, random, json, time, hashlib

BENIGN_PATTERNS = [
    "user login success", "health check ok", "metrics flush", "cache hit", "session renewed"
]
MALICIOUS_PATTERNS = [
    "failed password for root", "sudo privilege escalation", "XSS <script>",
    "SQLi UNION SELECT credit_card", "ransomware beacon", "C2 heartbeat" 
]

def gen_event(ts:int, text:str, malicious:bool):
    return {
        "ts": ts,
        "id": hashlib.sha1(f"{ts}-{text}".encode()).hexdigest()[:12],
        "payload": text,
        "malicious": malicious
    }

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--count", type=int, default=500)
    ap.add_argument("--malicious-ratio", type=float, default=0.15)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--labels", action="store_true", help="Emit labels JSON lines instead of raw payloads")
    marker_group = ap.add_mutually_exclusive_group()
    marker_group.add_argument("--include-marker", dest="include_marker", action="store_true", help="Include MALICIOUS marker prefix in malicious payloads (default)")
    marker_group.add_argument("--no-marker", dest="include_marker", action="store_false", help="Omit MALICIOUS marker prefix")
    ap.set_defaults(include_marker=True)
    args = ap.parse_args()
    random.seed(args.seed)
    out_lines = []
    now = int(time.time()*1000)
    for i in range(args.count):
        is_mal = random.random() < args.malicious_ratio
        if is_mal:
            base = random.choice(MALICIOUS_PATTERNS)
            noise = random.choice(["", " from 10.0.%d.%d" % (random.randint(1,254), random.randint(1,254))])
            # Optionally inject marker token for ground truth alignment with detection metrics
            if args.include_marker:
                base = f"MALICIOUS {base}"
        else:
            base = random.choice(BENIGN_PATTERNS)
            noise = ""
        evt = gen_event(now + i, base+noise, is_mal)
        if args.labels:
            out_lines.append(json.dumps(evt))
        else:
            out_lines.append(evt["payload"])  # raw payload only
    print("\n".join(out_lines))

if __name__ == "__main__":
    main()
