#!/usr/bin/env python3
"""policy-check.py <incident-dir> [--policy FILE] [--response FILE]

Validates the responder output against response.schema.json (stdlib subset) and
decides allow / escalate against autonomy-policy.yaml. Writes decision.json,
audit.jsonl entries and (on escalate) escalation-packet.json.
--no-claim: do not create action.lock (dry runs).
Exit: 0 = allow (one automatic action, claimed via action.lock), 3 = escalate, 1 = internal error
(audited; the caller must treat anything but 0 as "do not act").
Global cooldown: after an automatic action is claimed, no other incident may claim one for
global_cooldown_seconds (policy, default 300; env GLOBAL_COOLDOWN_SECONDS overrides, 0 = off), stamped in
<incidents dir>/.last_action under flock, so flapping alerts or a listener restart cannot loop restarts.
Env: COMPOSE_FILES (colon list; COMPOSE_FILE fallback) and COMPOSE_PROJECT_NAME expand the {{COMPOSE_ARGS}}
placeholder of action templates.
The model never supplies a command: it proposes proposed_action.action_id (+ params). build_action() validates both
against autonomy-policy.yaml and builds argv/env itself; decision.json carries action_id, params, command (display
string), argv and env, and the executor (run-responder.sh) runs only that argv/env. A legacy `command` field in the
response is refused (escalate).
"""
import json, math, os, re, shlex, sys, time, datetime
try:
    import fcntl
except ImportError:  # non-POSIX: cooldown still works, without the cross-process lock
    fcntl = None

HERE = os.path.dirname(os.path.abspath(__file__))


def _reject_constant(name):
    raise ValueError("non-finite number %s" % name)


def strict_json(text):
    """json.loads that refuses the non-standard NaN/Infinity/-Infinity tokens (they would defeat every numeric comparison)."""
    return json.loads(text, parse_constant=_reject_constant)


def too_deep(obj, limit=16):
    """Iterative nesting check (no recursion): the responder schema is shallow; anything deeper is hostile or broken."""
    stack = [(obj, 1)]
    while stack:
        o, d = stack.pop()
        if isinstance(o, (dict, list)):
            if d > limit:
                return True
            stack.extend((v, d + 1) for v in (o.values() if isinstance(o, dict) else o))
    return False


def load_policy(path):
    # Policy file is JSON-compatible YAML. Use PyYAML only if present, else JSON.
    text = open(path).read()
    try:
        return strict_json(text)
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
        if not math.isfinite(inst):
            return ["non-finite number"]   # 1e999 parses to inf
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


def compose_argv():
    """['-p', NAME, '-f', a, '-f', b] from env, or None (fail closed) when no compose file list is configured."""
    lst = os.environ.get("COMPOSE_FILES") or os.environ.get("COMPOSE_FILE") or ""
    if not lst or not re.fullmatch(r"[A-Za-z0-9_./:-]+", lst) or ".." in lst:
        return None
    if lst.startswith(":") or lst.endswith(":") or "::" in lst or lst.startswith("-") or ":-" in lst:
        return None   # empty segment or an entry that looks like a flag
    proj = os.environ.get("COMPOSE_PROJECT_NAME", "")
    if proj and not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", proj):
        return None
    return (["-p", proj] if proj else []) + [x for f in lst.split(":") if f for x in ("-f", f)]


# Param values: strict allowlist of characters on top of the per-param rule; no whitespace, no shell syntax, no leading "-".
SAFE_PARAM = re.compile(r"[A-Za-z0-9_./:=@%+][A-Za-z0-9_./:=@%+-]{0,119}")
ENV_NAME = re.compile(r"[A-Z_][A-Z0-9_]*")


def build_action(act, policy):
    """Validate action_id/params and build the command. Returns (spec, built, reasons); built = {action_id, params,
    argv, env, command} or None. Never raises on model input; every problem is a reason (fail closed)."""
    aid, typ, params = act.get("action_id"), act.get("type"), act.get("params", {})
    allowed = policy["allowed_actions"]
    if not isinstance(aid, str) or aid not in allowed:
        return None, None, ["unknown action_id %s: not in the policy allowlist (%s)" % (json.dumps(str(aid)[:60]), ", ".join(sorted(allowed)))]
    spec = allowed[aid]
    if typ not in spec["types"]:
        return spec, None, ["action_id %s is not allowed for incident type %s (allowed: %s)" % (aid, typ, ", ".join(spec["types"]))]
    if not isinstance(params, dict):
        return spec, None, ["params must be an object"]
    rules, reasons = spec.get("params", {}), []
    for k in params:
        if k not in rules:
            reasons.append("unknown param %s for action_id %s" % (json.dumps(str(k)[:40]), aid))
    for k, rule in rules.items():
        if k not in params:
            reasons.append("missing param %s for action_id %s" % (k, aid)); continue
        v = params[k]
        if not isinstance(v, str):
            reasons.append("param %s must be a string" % k)
        elif not SAFE_PARAM.fullmatch(v) or v.startswith("-"):
            reasons.append("param %s has a forbidden value (whitespace/shell metacharacters/leading dash are never allowed)" % k)
        elif "enum" in rule and v not in rule["enum"]:
            reasons.append("param %s not in its allowed values" % k)
        elif "regex" in rule and not re.fullmatch(rule["regex"], v):
            reasons.append("param %s does not match its rule" % k)
        elif any(re.search(d["regex"], v) for d in policy["denied_patterns"]):
            reasons.append("param %s hit a denied pattern" % k)
    if reasons:
        return spec, None, reasons
    ca = None
    argv = []
    for el in spec["argv"]:
        if el == "{{COMPOSE_ARGS}}":
            ca = compose_argv()
            if ca is None:
                return spec, None, ["COMPOSE_FILES not set or invalid: cannot build the command (fail closed)"]
            argv += ca
        elif re.fullmatch(r"\{\{param:([A-Za-z0-9_]+)\}\}", el):
            argv.append(params[el[8:-2]])
        elif "{{" in el:
            return spec, None, ["policy template element not understood: " + el]
        else:
            argv.append(el)
    env = dict(spec.get("env", {}))
    if not all(ENV_NAME.fullmatch(k) and isinstance(v, str) and SAFE_PARAM.fullmatch(v or "x") for k, v in env.items()):
        return spec, None, ["policy env entry invalid"]
    command = " ".join(["%s=%s" % (k, shlex.quote(v) if v else "") for k, v in env.items()] + [shlex.quote(x) for x in argv])
    return spec, {"action_id": aid, "params": params, "argv": argv, "env": env, "command": command}, []


def cooldown_seconds(policy):
    """Global cooldown between automatic actions of ANY incident. Env GLOBAL_COOLDOWN_SECONDS overrides the policy; 0 = off."""
    try:
        return max(0, int(os.environ.get("GLOBAL_COOLDOWN_SECONDS", policy.get("global_cooldown_seconds", 300))))
    except (TypeError, ValueError, OverflowError):
        return 300   # unparseable -> keep the safe default


def last_action_path(inc):
    return os.path.join(os.path.dirname(os.path.abspath(inc)), ".last_action")


def cooldown_state(inc, policy):
    """(seconds left, unreadable). Seconds left of the global cooldown (0 = free). Only a missing file (FileNotFoundError) is 'free';
    any other problem (permissions, I/O, garbled or non-finite content) fails closed with the full cooldown. A timestamp in the future keeps the cooldown
    running until ts + cooldown, and the reported seconds are exactly that."""
    cd = cooldown_seconds(policy)
    if cd <= 0:
        return 0, False
    try:
        ts = float(open(last_action_path(inc)).read().split()[0])
    except FileNotFoundError:
        return 0, False
    except OSError:
        return cd, True   # unreadable state: cannot prove the cooldown is over -> fail closed
    except (ValueError, IndexError):
        return cd, True   # garbled/empty stamp: cannot prove the cooldown is over -> fail closed
    if not math.isfinite(ts):
        return cd, True   # nan/inf: same
    return max(0, int(cd - (time.time() - ts) + 0.999)), False


def cooldown_left(inc, policy):
    return cooldown_state(inc, policy)[0]


def cooldown_reason(policy, left, unreadable):
    if unreadable:
        return "cooldown state unreadable: .last_action cannot be read, so the global cooldown cannot be proven over (fail closed)"
    return "global action cooldown: another automatic action ran less than %ds ago (%ds left)" % (cooldown_seconds(policy), left)


def claim_cooldown(inc, policy):
    """Atomically (flock) check and stamp .last_action. Returns (seconds left, unreadable); (0, False) = claimed."""
    cd = cooldown_seconds(policy)
    if cd <= 0:
        return 0, False
    path = last_action_path(inc)
    lf = open(path + ".lock", "a")
    try:
        if fcntl:
            fcntl.flock(lf, fcntl.LOCK_EX)
        left, unreadable = cooldown_state(inc, policy)
        if left:
            return left, unreadable
        tmp = path + ".tmp%d" % os.getpid()
        with open(tmp, "w") as f:
            f.write("%d %s\n" % (time.time(), os.path.basename(os.path.abspath(inc))))
        os.replace(tmp, path)
        return 0, False
    finally:
        lf.close()


def decide(resp, policy, schema, inc, load_err=None):
    """Returns (verdict, reasons, built). built is the command built by the policy (only when verdict is allow)."""
    reasons = []
    if resp is None and load_err:
        return "escalate", ["schema: response unreadable: " + load_err], None
    act0 = resp.get("proposed_action") if isinstance(resp, dict) else None
    if isinstance(act0, dict):   # clear reasons for the new contract before the generic schema errors
        if "command" in act0:
            return "escalate", ["legacy command field refused: the responder must propose action_id (and params), never a shell command"], None
        if "action_id" not in act0:
            return "escalate", ["missing action_id in proposed_action (use \"none\" for code_fix_pr/escalate)"], None
        aid = act0["action_id"]
        if aid != "none" and (not isinstance(aid, str) or aid not in policy["allowed_actions"]):
            return "escalate", ["unknown action_id %s: not in the policy allowlist (%s)" % (json.dumps(str(aid)[:60]), ", ".join(sorted(policy["allowed_actions"])))], None
    schema = json.loads(json.dumps(schema))   # the policy is authoritative: its allowlist (+ "none") is the action_id enum
    schema["properties"]["proposed_action"]["properties"]["action_id"]["enum"] = sorted(policy["allowed_actions"]) + ["none"]
    errs = validate(resp, schema) if isinstance(resp, dict) else ["response is not an object"]
    if errs:
        return "escalate", ["schema: " + e for e in errs], None
    act = resp["proposed_action"]
    typ, aid = act["type"], act["action_id"]
    built = None
    if resp["needs_human"]:
        reasons.append("responder set needs_human")
    conf, thr = resp["confidence"], policy.get("confidence_threshold")
    thr_ok = isinstance(thr, (int, float)) and not isinstance(thr, bool) and math.isfinite(thr)
    if not thr_ok:
        reasons.append("policy confidence_threshold is not a finite number")
    elif not (isinstance(conf, (int, float)) and not isinstance(conf, bool) and math.isfinite(conf) and conf >= thr):
        reasons.append("confidence %s is not a finite number >= threshold %.2f" % (conf, thr))
    if typ in policy["propose_only_types"]:
        reasons.append("action type %s is propose-only" % typ)
        if aid != "none" or act.get("params"):
            reasons.append("propose-only type %s must use action_id none and no params" % typ)
    elif aid == "none":
        reasons.append("action_id none is not executable for type %s" % typ)
    else:
        _spec, built, r = build_action(act, policy)
        reasons += r
    if os.path.exists(os.path.join(inc, "action.lock")):
        reasons.append("an automatic action was already taken for this incident")
    left, unreadable = cooldown_state(inc, policy)
    if left:
        reasons.append(cooldown_reason(policy, left, unreadable))
    return ("escalate" if reasons else "allow"), reasons, (None if reasons else built)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    inc = a[0]
    os.makedirs(inc, exist_ok=True)
    opt = lambda n, d: a[a.index(n) + 1] if n in a else d
    policy = load_policy(opt("--policy", os.path.join(HERE, "autonomy-policy.yaml")))
    schema = strict_json(open(os.path.join(HERE, "response.schema.json")).read())
    rpath = opt("--response", os.path.join(inc, "response.json"))
    load_err = None
    try:
        resp = strict_json(open(rpath).read())
        if too_deep(resp):
            raise ValueError("response is nested too deeply")
    except (Exception, RecursionError) as e:
        resp, load_err = None, ("%s: %s" % (type(e).__name__, e))[:200]
        audit(inc, "policy_input_error", load_err)
    verdict, reasons, built = decide(resp, policy, schema, inc, load_err)
    if verdict == "allow" and "--no-claim" not in a and policy["max_automatic_actions_per_incident"] >= 1:
        try:  # atomic claim: at most one automatic action
            fd = os.open(os.path.join(inc, "action.lock"), os.O_CREAT | os.O_EXCL | os.O_WRONLY)
            os.write(fd, json.dumps(built).encode()); os.close(fd)   # the claimed action; the executor runs exactly this
        except FileExistsError:
            verdict, reasons, built = "escalate", ["an automatic action was already taken for this incident"], None
        else:
            left, unreadable = claim_cooldown(inc, policy)   # global, across incidents; release the per-incident claim if refused
            if left:
                os.unlink(os.path.join(inc, "action.lock"))
                verdict, reasons, built = "escalate", [cooldown_reason(policy, left, unreadable)], None
    pa = resp.get("proposed_action") if isinstance(resp, dict) else None
    pa = pa if isinstance(pa, dict) else {}
    b = built or {}
    decision = {"incident_id": os.path.basename(os.path.abspath(inc)), "decision": verdict, "reasons": reasons,
                "action_id": b.get("action_id", pa.get("action_id") if isinstance(pa.get("action_id"), str) else None),
                "params": b.get("params", pa.get("params") if isinstance(pa.get("params"), dict) else None),
                "command": b.get("command"), "argv": b.get("argv"), "env": b.get("env"),
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
