#!/usr/bin/env python3
"""Validate run files against findings.schema.json. stdlib only.
Supports the keywords the schema uses: type (incl. lists), enum, required, properties,
items, $ref (#/$defs/..), pattern, minimum, format=date. Unknown keywords are ignored.
Usage: validate-findings.py [file ...]   (default: runs/*.json next to this script)"""
import json, re, sys, glob, os, datetime

HERE = os.path.dirname(os.path.abspath(__file__))
TYPES = {"object": dict, "array": list, "string": str, "null": type(None),
         "boolean": bool, "integer": int, "number": (int, float)}

def is_type(v, t):
    if t in ("integer", "number") and isinstance(v, bool):
        return False
    return isinstance(v, TYPES[t])

def resolve(ref, root):
    node = root
    for p in ref.lstrip("#/").split("/"):
        node = node[p]
    return node

def check(v, s, root, path, errs):
    if "$ref" in s:
        s = resolve(s["$ref"], root)
    if "type" in s:
        ts = s["type"] if isinstance(s["type"], list) else [s["type"]]
        if not any(is_type(v, t) for t in ts):
            errs.append(f"{path}: expected {ts}, got {type(v).__name__}"); return
    if "enum" in s and v not in s["enum"]:
        errs.append(f"{path}: {v!r} not in {s['enum']}")
    if isinstance(v, str):
        if "pattern" in s and not re.search(s["pattern"], v):
            errs.append(f"{path}: {v!r} does not match {s['pattern']}")
        if s.get("format") == "date":
            try: datetime.date.fromisoformat(v)
            except ValueError: errs.append(f"{path}: {v!r} not a date")
    if isinstance(v, (int, float)) and not isinstance(v, bool) and "minimum" in s and v < s["minimum"]:
        errs.append(f"{path}: {v} < minimum {s['minimum']}")
    if isinstance(v, dict):
        for r in s.get("required", []):
            if r not in v: errs.append(f"{path}: missing required '{r}'")
        for k, sub in s.get("properties", {}).items():
            if k in v: check(v[k], sub, root, f"{path}.{k}", errs)
    if isinstance(v, list) and "items" in s:
        for i, x in enumerate(v): check(x, s["items"], root, f"{path}[{i}]", errs)

def main(argv):
    schema = json.load(open(os.path.join(HERE, "findings.schema.json")))
    files = argv or sorted(glob.glob(os.path.join(HERE, "runs", "*.json")))
    bad = 0
    for f in files:
        errs = []
        try: check(json.load(open(f)), schema, schema, "$", errs)
        except (OSError, ValueError) as e: errs.append(f"unreadable: {e}")
        print(("FAIL " if errs else "OK   ") + os.path.relpath(f))
        for e in errs: print("   ", e)
        bad += bool(errs)
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
