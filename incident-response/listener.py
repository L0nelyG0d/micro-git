#!/usr/bin/env python3
"""Alertmanager webhook listener (stdlib only). POST /alert on :9099.

Pipeline per firing alert (observe -> reason -> authorize -> act -> verify -> remember):
  incidents/<id>/alert.json -> collect-evidence.sh -> run-responder.sh
  (which calls policy-check.py, executes ONLY an allowlisted command, then verify-recovery.sh)
  -> verified recovery, or escalation-packet.json / escalation.json for a human.

Auth: POST /alert requires `Authorization: Bearer <token>` (constant-time compare). The token is read per request
from ALERT_TOKEN_FILE (default incident-response/.alert-token, git-ignored; create with make-token.sh). No token
file / empty file = every alert is rejected (fail closed). Content-Type must be application/json.
Env: ALERT_TOKEN_FILE MAX_PIPELINES (2) LISTEN_HOST (127.0.0.1) LISTEN_PORT (9099) AUTO_EXECUTE (0 = dry-run: log the planned action, change nothing; 1 = run the allowlisted action) RESPONDER (auto) INCIDENTS_DIR
No shell is used: arguments are passed as lists and id/route are validated first.
"""
import collections, shutil, signal, tempfile, datetime, hashlib, hmac, json, os, re, secrets, subprocess, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
INCIDENTS = os.environ.get("INCIDENTS_DIR", os.path.join(HERE, "incidents"))
ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$")
ROUTE_RE = re.compile(r"^[A-Za-z0-9_/.:{}*-]{1,120}$")
MAX_BODY = 1 << 20
# Safe default: dry-run. Only the exact value "1" enables executing an allowlisted action.
AUTO_EXECUTE = "1" if os.environ.get("AUTO_EXECUTE", "0") == "1" else "0"
MAX_SEEN = 1000
_seen, _lock = collections.OrderedDict(), threading.Lock()   # bounded dedupe of Alertmanager repeats
_pipelines = threading.BoundedSemaphore(int(os.environ.get("MAX_PIPELINES", "2")))
TOKEN_FILE = os.environ.get("ALERT_TOKEN_FILE", os.path.join(HERE, ".alert-token"))


def token():
    try:
        return open(TOKEN_FILE).read().strip()
    except OSError:
        return ""


_perm_warned = False   # token-permission warning is emitted once per process


def _gappend(kind, detail, decision):
    with open(os.path.join(INCIDENTS, "listener-audit.jsonl"), "a") as f:
        f.write(json.dumps({"ts": now(), "actor": "listener.py", "kind": kind, "detail": detail, "decision": decision}) + "\n")


def gaudit(kind, detail, decision="n/a"):
    """Listener-level audit (requests that never became an incident)."""
    global _perm_warned
    os.makedirs(INCIDENTS, exist_ok=True)
    if not _perm_warned:
        _perm_warned = True   # set first: the warning must never trigger itself
        try:
            if os.stat(TOKEN_FILE).st_mode & 0o077:
                _gappend("config", "ALERT_TOKEN_FILE %s is readable by group/others; run chmod 600 on it" % TOKEN_FILE, "warn")
                print("WARNING: alert token file is group/other accessible; chmod 600 it", file=sys.stderr)
        except OSError:
            pass
    _gappend(kind, detail, decision)


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def audit(inc, kind, detail, decision="n/a"):
    os.makedirs(inc, exist_ok=True)
    with open(os.path.join(inc, "audit.jsonl"), "a") as f:
        f.write(json.dumps({"ts": now(), "actor": "listener.py", "kind": kind,
                            "detail": detail, "decision": decision}) + "\n")


def _remove_clean_copy(inc):
    """run-responder.sh records its temp git-archive copy in <inc>/.cleanrepo; remove it if the script was killed."""
    try:
        path = open(os.path.join(inc, ".cleanrepo")).read().strip()
        real, tmp = os.path.realpath(path), os.path.realpath(tempfile.gettempdir())
        if path and real.startswith(tmp + os.sep) and os.path.isdir(real):
            shutil.rmtree(real, ignore_errors=True)
        os.remove(os.path.join(inc, ".cleanrepo"))
    except OSError:
        pass


def run(inc, argv, env_extra=None, timeout=int(os.environ.get("PIPELINE_TIMEOUT", "1800"))):
    env = dict(os.environ, **(env_extra or {}))
    audit(inc, "command", " ".join(argv))
    p = None
    try:
        # own session/process group so a timeout can take down the whole tree (claude, docker, curl ...)
        p = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             text=True, env=env, start_new_session=True)
        try:
            p.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            audit(inc, "command_timeout", "%s exceeded %ds: killing process group" % (os.path.basename(argv[0]), timeout))
            for sig in (signal.SIGTERM, signal.SIGKILL):
                try:
                    os.killpg(p.pid, sig)
                except ProcessLookupError:
                    break
                try:
                    p.communicate(timeout=10)
                    break
                except subprocess.TimeoutExpired:
                    continue
            _remove_clean_copy(inc)
            return 98
        audit(inc, "command_result", "%s rc=%d" % (os.path.basename(argv[0]), p.returncode))
        _remove_clean_copy(inc)
        return p.returncode
    except Exception as e:  # fail soft
        audit(inc, "command_error", "%s: %s" % (os.path.basename(argv[0]), e))
        return 99


def escalate(inc, iid, why):
    pkt = os.path.join(inc, "escalation.json")
    json.dump({"incident_id": iid, "why_escalated": [why], "audit_log": os.path.join(inc, "audit.jsonl")},
              open(pkt, "w"), indent=2)
    audit(inc, "escalation", why, "escalate")


def handle_alert(alert):
    with _pipelines:   # at most MAX_PIPELINES run concurrently; others wait
        _handle_alert(alert)


def _handle_alert(alert):
    ctx = {}
    try:
        _run_alert(alert, ctx)
    except Exception as e:   # never leave an incident silently half-done
        gaudit("pipeline_error", "%s: %s" % (type(e).__name__, e), "error")
        if ctx.get("inc"):
            try:
                escalate(ctx["inc"], ctx["iid"], "listener internal error: %s" % type(e).__name__)
            except Exception:
                pass


def _run_alert(alert, ctx):
    if not isinstance(alert, dict):
        return gaudit("alert_rejected", "alert is not an object", "reject")
    labels = alert.get("labels")
    if not isinstance(labels, dict):
        return gaudit("alert_rejected", "labels is not an object (%s)" % type(labels).__name__, "reject")
    route = labels.get("route", "")
    if not isinstance(route, str):
        return gaudit("alert_rejected", "route label is not a string", "reject")
    fp = alert.get("fingerprint") or hashlib.sha256(json.dumps(alert, sort_keys=True).encode()).hexdigest()[:12]
    # unique, unguessable id (random suffix); the dir is created exclusively (never reuse) and private (0700)
    iid = "inc-%s-%s-%s" % (datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S"),
                            re.sub(r"[^A-Za-z0-9]", "", fp)[:16] or "x", secrets.token_hex(3))
    inc = os.path.join(INCIDENTS, iid)
    os.makedirs(INCIDENTS, exist_ok=True)
    os.makedirs(inc, mode=0o700, exist_ok=False)
    ctx["inc"], ctx["iid"] = inc, iid
    json.dump(alert, open(os.path.join(inc, "alert.json"), "w"), indent=2)
    audit(inc, "alert_received", "alertname=%s route=%s" % (labels.get("alertname"), route))
    audit(inc, "config", "effective AUTO_EXECUTE=%s (%s)" % (AUTO_EXECUTE, "actions will run" if AUTO_EXECUTE == "1" else "dry-run: planned action logged only"))
    if route == "all":
        return escalate(inc, iid, "route=all (no single route to act on); refusing to run automation")
    if not ID_RE.match(iid) or not ROUTE_RE.match(route) or ".." in route:
        return escalate(inc, iid, "alert has missing or invalid route label; refusing to run automation")
    if run(inc, [os.path.join(HERE, "collect-evidence.sh"), iid, route]) != 0:
        return escalate(inc, iid, "evidence collection failed")
    rc = run(inc, [os.path.join(HERE, "run-responder.sh"), iid, route],
             {"AUTO_EXECUTE": AUTO_EXECUTE, "INCIDENTS_DIR": INCIDENTS})
    if rc != 0:
        if not os.path.exists(os.path.join(inc, "escalation.json")):
            escalate(inc, iid, "responder/verification pipeline exited rc=%d" % rc)
    audit(inc, "incident_done", "rc=%d" % rc)


class H(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

    def do_GET(self):
        self._send(200, {"status": "ok"}) if self.path == "/healthz" else self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/alert":
            return self._send(404, {"error": "not found"})
        tok = token()
        got = self.headers.get("Authorization", "")
        if not tok or not got.startswith("Bearer ") or not hmac.compare_digest(got[7:].encode(), tok.encode()):
            gaudit("auth_rejected", "missing/invalid bearer token from %s" % self.client_address[0], "reject")
            return self._send(401, {"error": "unauthorized"})
        if not (self.headers.get("Content-Type") or "").split(";")[0].strip().lower() == "application/json":
            gaudit("request_rejected", "content-type is not application/json", "reject")
            return self._send(415, {"error": "application/json required"})
        try:
            n = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            gaudit("request_rejected", "non-integer Content-Length", "reject")
            return self._send(400, {"error": "bad Content-Length"})
        if n <= 0 or n > MAX_BODY:
            return self._send(413, {"error": "bad body size"})
        try:
            body = json.loads(self.rfile.read(n))
            alerts = [a for a in body.get("alerts", []) if isinstance(a, dict) and a.get("status") == "firing"]
        except Exception:
            return self._send(400, {"error": "invalid JSON"})
        started = 0
        for a in alerts:
            if not isinstance(a.get("fingerprint"), str) or not isinstance(a.get("startsAt"), str):
                gaudit("alert_rejected", "fingerprint/startsAt missing or not strings", "reject")
                continue
            key = (a.get("fingerprint"), a.get("startsAt"))
            with _lock:
                if key in _seen:
                    continue  # Alertmanager repeats notifications
                _seen[key] = 1
                while len(_seen) > MAX_SEEN:
                    _seen.popitem(last=False)
            threading.Thread(target=handle_alert, args=(a,), daemon=True).start()
            started += 1
        self._send(202, {"accepted": started})

    def log_message(self, fmt, *args):
        sys.stderr.write("listener: " + fmt % args + "\n")


if __name__ == "__main__":
    host, port = os.environ.get("LISTEN_HOST", "127.0.0.1"), int(os.environ.get("LISTEN_PORT", "9099"))
    os.makedirs(INCIDENTS, exist_ok=True)
    print("listening on %s:%d (POST /alert) AUTO_EXECUTE=%s" % (host, port, AUTO_EXECUTE), flush=True)
    ThreadingHTTPServer((host, port), H).serve_forever()
