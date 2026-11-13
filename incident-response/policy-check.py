#!/usr/bin/env python3
"""policy-check.py <incident-dir> [--policy FILE] [--response FILE]

Validates the responder output against response.schema.json (stdlib subset) and
decides allow / escalate against autonomy-policy.yaml. Writes decision.json,
audit.jsonl entries and (on escalate) escalation-packet.json.
--no-claim: do not create action.lock (dry runs).
Exit: 0 = allow (one automatic action, claimed via action.lock), 3 = escalate, 1 = internal error
(audited; the caller must treat anything but 0 as "do not act").
Env: COMPOSE_FILES (colon list; COMPOSE_FILE fallback) and COMPOSE_PROJECT_NAME expand the {{COMPOSE_ARGS}}
placeholder of command templates.
"""
import json, os, re, sys, datetime

HERE = os.path.dirname(os.path.abspath(__file__))


def load_policy(path):
    # Policy file is JSON-compatible YAML. Use PyYAML only if present, else JSON.
    text = open(path).read()
    try:
        return json.loads(text)
    except ValueError:
        try:
            import yaml  # optional
            return yaml.safe_load(text)
        except ImportError:
            sys.exit("policy must be JSON-compatible YAML when PyYAML is absent")


def validate(inst, schema):
    errs = []
    t = schema.get("type")
    tmap = {"object": dict, "array": list, "string": str, "boolean": bool}
    if t == "number":
        if isinstance(inst, bool) or not isinstance(inst, (int, float)):
            return ["expected number"]
    elif t in tmap and (not isinstance(inst, tmap[t]) or (t != "boolean" and isinstance(inst, bool))):
        return ["expected " + t]
    if "enum" in schema and inst not in schema["enum"]:
        errs.append("not in enum %s" % schema["enum"])
    if isinstance(inst, str):
        if "pattern" in schema and not re.search(schema["pattern"], inst):
            errs.append("pattern mismatch")
        if "maxLength" in schema and len(inst) > schema["maxLength"]:
            errs.append("too long")
    if isinstance(inst, (int, float)) and not isinstance(inst, bool):
        if "minimum" in schema and inst < schema["minimum"]:
            errs.append("below minimum")
        if "maximum" in schema and inst > schema["maximum"]:
            errs.append("above maximum")
    if isinstance(inst, list):
        if "minItems" in schema and len(inst) < schema["minItems"]:
            errs.append("too few items")
        for i, v in enumerate(inst):
            errs += ["[%d] %s" % (i, e) for e in validate(v, schema.get("items", {}))]
    if isinstance(inst, dict):
        props = schema.get("properties", {})
        for r in schema.get("required", []):
            if r not in inst:
                errs.append("missing " + r)
        if schema.get("additionalProperties") is False:
            errs += ["unexpected " + k for k in inst if k not in props]
        for k, v in inst.items():
            if k in props:
                errs += [k + ": " + e for e in validate(v, props[k])]
    return errs


def audit(inc, kind, detail, decision="n/a"):
    with open(os.path.join(inc, "audit.jsonl"), "a") as f:
        f.write(json.dumps({"ts": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                            "actor": "policy-check.py", "kind": kind, "detail": detail, "decision": decision}) + "\n")


def compose_args():
    """'-p NAME -f a -f b' from env, or None (fail closed) when no compose file list is configured."""
    lst = os.environ.get("COMPOSE_FILES") or os.environ.get("COMPOSE_FILE") or ""
    if not lst or not re.fullmatch(r"[A-Za-z0-9_./:-]+", lst) or ".." in lst:
        return None
    if lst.startswith(":") or lst.endswith(":") or "::" in lst or lst.startswith("-") or ":-" in lst:
        return None   # empty segment or an entry that looks like a flag
    proj = os.environ.get("COMPOSE_PROJECT_NAME", "")
    if proj and not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", proj):
        return None
    parts = (["-p", proj] if proj else []) + [x for f in lst.split(":") if f for x in ("-f", f)]
    return " ".join(parts)


def expand(template):
    if "{{COMPOSE_ARGS}}" not in template:
        return template
    ca = compose_args()
    return None if ca is None else template.replace("{{COMPOSE_ARGS}}", re.escape(ca).replace("\\ ", " "))


def decide(resp, policy, schema, inc):
    reasons = []
    errs = validate(resp, schema) if isinstance(resp, dict) else ["response is not an object"]
    if errs:
        return "escalate", ["schema: " + e for e in errs]
    act = resp["proposed_action"]
    cmd, typ = act["command"], act["type"]
    if cmd != cmd.strip():
        reasons.append("command has leading/trailing whitespace")
    if resp["needs_human"]:
        reasons.append("responder set needs_human")
    if resp["confidence"] < policy["confidence_threshold"]:
        reasons.append("confidence %.2f < threshold %.2f" % (resp["confidence"], policy["confidence_threshold"]))
    if typ in policy["propose_only_types"]:
        reasons.append("action type %s is propose-only" % typ)
    else:
        for d in policy["denied_patterns"]:
            if re.search(d["regex"], cmd):
                reasons.append("denied pattern: " + d["id"])
        match = []
        for a in policy["allowed_actions"]:
            rx = expand(a["command_regex"])
            if rx is not None and a["type"] == typ and re.fullmatch(rx, cmd):
                match.append(a)
        if not match:
            reasons.append("command not in allowlist for type %s%s" % (typ, "" if compose_args() else " (COMPOSE_FILES not set: fail closed)"))
    if os.path.exists(os.path.join(inc, "action.lock")):
        reasons.append("an automatic action was already taken for this incident")
    return ("escalate" if reasons else "allow"), reasons


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    inc = a[0]
    os.makedirs(inc, exist_ok=True)
    opt = lambda n, d: a[a.index(n) + 1] if n in a else d
    policy = load_policy(opt("--policy", os.path.join(HERE, "autonomy-policy.yaml")))
    schema = json.load(open(os.path.join(HERE, "response.schema.json")))
    rpath = opt("--response", os.path.join(inc, "response.json"))
    try:
        resp = json.load(open(rpath))
    except Exception as e:
        resp = None
        audit(inc, "policy_input_error", str(e))
    verdict, reasons = decide(resp, policy, schema, inc)
    if verdict == "allow" and "--no-claim" not in a and policy["max_automatic_actions_per_incident"] >= 1:
        try:  # atomic claim: at most one automatic action
            fd = os.open(os.path.join(inc, "action.lock"), os.O_CREAT | os.O_EXCL | os.O_WRONLY)
            os.write(fd, resp["proposed_action"]["command"].encode()); os.close(fd)
        except FileExistsError:
            verdict, reasons = "escalate", ["an automatic action was already taken for this incident"]
    decision = {"incident_id": os.path.basename(os.path.abspath(inc)), "decision": verdict, "reasons": reasons,
                "command": (resp or {}).get("proposed_action", {}).get("command") if isinstance(resp, dict) else None,
                "policy_version": policy["version"]}
    json.dump(decision, open(os.path.join(inc, "decision.json"), "w"), indent=2)
    audit(inc, "policy_decision", json.dumps(decision), verdict)
    if verdict == "escalate":
        packet = {"incident_id": decision["incident_id"], "why_escalated": reasons, "responder_output": resp,
                  "alert": _load(os.path.join(inc, "alert.json")),
                  "evidence_manifest": _load(os.path.join(inc, "evidence-manifest.json")),
                  "audit_log": os.path.join(inc, "audit.jsonl"),
                  "next_step": "Human: review responder_output and evidence, then act manually and run runbooks/verify-recovery.sh <route>."}
        json.dump(packet, open(os.path.join(inc, policy["escalation"]["packet_file"]), "w"), indent=2)
        audit(inc, "escalation_packet", policy["escalation"]["packet_file"])
    print(json.dumps(decision))
    sys.exit(0 if verdict == "allow" else 3)


def _load(p):
    try:
        return json.load(open(p))
    except Exception:
        return None


if __name__ == "__main__":
    try:
        main()
    except SystemExit as e:
        if e.code in (0, 3):
            raise
        try:
            audit(sys.argv[1], "policy_error", "exit: %s" % e.code, "error")
        except Exception:
            pass
        print("policy-check error: %s" % e.code, file=sys.stderr)
        sys.exit(1)
    except BaseException as e:  # never let a crash look like an allow or a clean escalation
        try:
            audit(sys.argv[1], "policy_error", "%s: %s" % (type(e).__name__, e), "error")
        except Exception:
            pass
        print("policy-check error: %s" % e, file=sys.stderr)
        sys.exit(1)
