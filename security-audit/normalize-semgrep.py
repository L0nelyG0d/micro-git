#!/usr/bin/env python3
"""Convert raw `semgrep --json` output into a schema-valid run file (stdlib only).
Usage: normalize-semgrep.py RAW.json RUN_ID DATE OUT.json [COMMIT]
Ids are deterministic: SEC-<DATE as YYYYMMDD>-<first 8 hex of sha1(rule|file|line)>.
Severity map: ERROR->high, WARNING->medium, INFO->low. Disposition always starts 'open'."""
import json, sys, hashlib, os, importlib.util

_RP = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "incident-response", "redact.py")
_spec = importlib.util.spec_from_file_location("ir_redact", _RP); _mod = importlib.util.module_from_spec(_spec); _spec.loader.exec_module(_mod)
redact = _mod.redact   # same redaction as the incident pipeline, applied to scanner messages before they are committed

SEV = {"ERROR": "high", "WARNING": "medium", "INFO": "low"}

def fid(date, rule, file, line):
    h = hashlib.sha1(f"{rule}|{file}|{line}".encode()).hexdigest()[:8]  # not security use
    return f"SEC-{date.replace('-', '')}-{h}"

def rel(p):
    for pre in ("/src/", "src/"):  # docker mount point; keeps ids identical to local runs
        if p.startswith(pre): return p[len(pre):]
    return p

def main(raw, run_id, date, out, commit=None):
    try: d = json.load(open(raw))
    except (OSError, ValueError): d = {}
    if d.get("status") == "scanner_unavailable" or "results" not in d:
        check = {"name": "semgrep", "status": "scanner_unavailable", "summary": "no usable semgrep output"}
        findings = []
    else:
        findings = []
        for r in sorted(d["results"], key=lambda r: (r["check_id"], rel(r["path"]), r["start"]["line"])):
            ex = r.get("extra", {})
            findings.append({
                "id": fid(date, r["check_id"], rel(r["path"]), r["start"]["line"]),
                "source": "scanner", "rule": r["check_id"],
                "severity": SEV.get(ex.get("severity", ""), "info"),
                "file": rel(r["path"]), "line": r["start"]["line"],
                "description": redact((ex.get("message") or ""))[:200],
                "evidence": "semgrep (rule match; code excerpt omitted)",
                "disposition": "open", "reviewer": None, "date": None, "related_incident": None})
        check = {"name": "semgrep", "status": "findings" if findings else "ok",
                 "summary": f"{len(findings)} finding(s); dispositions pending human review"}
    run = {"run_id": run_id, "date": date, "checks": [check], "findings": findings}
    if commit: run["commit"] = commit
    json.dump(run, open(out, "w"), indent=2); open(out, "a").write("\n")

if __name__ == "__main__":
    main(*sys.argv[1:])
