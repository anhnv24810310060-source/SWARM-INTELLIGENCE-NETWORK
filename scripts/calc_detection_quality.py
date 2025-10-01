#!/usr/bin/env python3
"""Compute precision / recall / F1 from detection log and labeled synthetic JSON lines.
Usage:
  python scripts/calc_detection_quality.py --detections detections.log --labeled labeled.jsonl
Assumes detection log lines are JSON with optional rule_id and payload_hash.
Labeled file lines: {"payload": str OR "payload" key implied, "malicious": bool}
"""
import argparse, json, sys, os, datetime, hashlib
from typing import Dict, Set

def load_detections(path: str) -> tuple[Set[str], Dict[str, str]]:
    """Load detection events and return set of identifiers + hash->severity mapping."""
    ids = set()
    severity_map = {}  # payload_hash -> severity
    try:
        with open(path,'r') as f:
            for line in f:
                line=line.strip()
                if not line: continue
                try:
                    obj=json.loads(line)
                    # Prefer payload_hash for exact matching; fallback to preview for backward compat
                    identifier = obj.get('payload_hash') or obj.get('payload_preview') or ''
                    if identifier:
                        ids.add(identifier)
                        severity = obj.get('severity', 'medium')
                        severity_map[identifier] = severity
                except Exception:
                    continue
    except FileNotFoundError:
        pass
    return ids, severity_map

def load_labeled(path: str):
    """Load labeled dataset and compute SHA-256 hash for each payload."""
    data=[]
    with open(path,'r') as f:
        for line in f:
            if not line.strip(): continue
            obj=json.loads(line)
            payload=obj.get('payload') or obj.get('payload_preview') or ''
            mal=bool(obj.get('malicious'))
            # Compute hash to match detection events
            payload_hash = hashlib.sha256(payload.encode('utf-8')).hexdigest()
            data.append((payload, payload_hash, mal))
    return data

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--detections', required=True)
    ap.add_argument('--labeled', required=True)
    ap.add_argument('--csv', help='Optional path to append metrics as CSV (header auto-added).')
    ap.add_argument('--weighted', action='store_true', help='Compute severity-weighted F1 score')
    args=ap.parse_args()
    detected, severity_map = load_detections(args.detections)
    labeled = load_labeled(args.labeled)
    
    # Severity weights for weighted F1 calculation
    SEVERITY_WEIGHTS = {'critical': 3.0, 'high': 2.0, 'medium': 1.0, 'low': 0.5, 'info': 0.5}
    
    tp=fp=fn=0
    weighted_tp = weighted_fp = weighted_fn = 0.0
    
    for payload, payload_hash, mal in labeled:
        # Prefer exact hash matching; fallback to preview heuristics for backward compatibility
        matched_id = None
        if payload_hash in detected:
            matched_id = payload_hash
        else:
            for d in detected:
                if payload.startswith(d) or d.startswith(payload[:60]):
                    matched_id = d
                    break
        
        hit = matched_id is not None
        severity = severity_map.get(matched_id, 'medium') if matched_id else 'medium'
        weight = SEVERITY_WEIGHTS.get(severity.lower(), 1.0)
        
        if mal and hit:
            tp += 1
            weighted_tp += weight
        elif mal and not hit:
            fn += 1
            weighted_fn += weight
        elif not mal and hit:
            fp += 1
            weighted_fp += weight
    
    precision = tp / (tp+fp) if (tp+fp)>0 else 0.0
    recall = tp / (tp+fn) if (tp+fn)>0 else 0.0
    f1 = (2*precision*recall)/(precision+recall) if (precision+recall)>0 else 0.0
    
    # Weighted metrics
    w_precision = weighted_tp / (weighted_tp + weighted_fp) if (weighted_tp + weighted_fp) > 0 else 0.0
    w_recall = weighted_tp / (weighted_tp + weighted_fn) if (weighted_tp + weighted_fn) > 0 else 0.0
    w_f1 = (2*w_precision*w_recall)/(w_precision+w_recall) if (w_precision+w_recall)>0 else 0.0
    
    summary = {
        'true_positives': tp,
        'false_positives': fp,
        'false_negatives': fn,
        'precision': round(precision,4),
        'recall': round(recall,4),
        'f1': round(f1,4)
    }
    
    if args.weighted:
        summary['weighted_precision'] = round(w_precision, 4)
        summary['weighted_recall'] = round(w_recall, 4)
        summary['weighted_f1'] = round(w_f1, 4)
    
    print(json.dumps(summary, indent=2))

    if args.csv:
        # Ensure directory exists
        os.makedirs(os.path.dirname(args.csv) or '.', exist_ok=True)
        file_exists = os.path.isfile(args.csv)
        if args.weighted:
            header = 'timestamp,true_positives,false_positives,false_negatives,precision,recall,f1,weighted_precision,weighted_recall,weighted_f1'\
                if not file_exists else None
            ts = datetime.datetime.utcnow().replace(microsecond=0).isoformat()+'Z'
            row = f"{ts},{tp},{fp},{fn},{summary['precision']},{summary['recall']},{summary['f1']},{summary['weighted_precision']},{summary['weighted_recall']},{summary['weighted_f1']}"
        else:
            header = 'timestamp,true_positives,false_positives,false_negatives,precision,recall,f1'\
                if not file_exists else None
            ts = datetime.datetime.utcnow().replace(microsecond=0).isoformat()+'Z'
            row = f"{ts},{tp},{fp},{fn},{summary['precision']},{summary['recall']},{summary['f1']}"
        with open(args.csv, 'a') as cf:
            if header:
                cf.write(header+'\n')
            cf.write(row+'\n')
        # Emit a concise stderr note for CI logs
        print(f"[quality] Appended metrics row to {args.csv}", file=sys.stderr)

if __name__ == '__main__':
    main()
