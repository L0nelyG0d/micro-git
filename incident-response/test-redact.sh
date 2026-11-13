#!/usr/bin/env bash
# test-redact.sh : redact() must remove secrets and keep valid JSON valid. No stack needed.
set -uo pipefail
D="$(cd "$(dirname "$0")" && pwd)"
python3 -I - "$D" <<'PY'
import json, subprocess, sys
D = sys.argv[1]
def red(t): return subprocess.run(["python3", "-I", D + "/redact.py"], input=t, capture_output=True, text=True).stdout
def esc(o): return json.dumps(json.dumps(o))[1:-1]   # JSON escaped inside a string, as Loki returns it
cases = [  # (name, input, secrets that must vanish, input_is_json)
 ("env POSTGRES_PASSWORD", "POSTGRES_PASSWORD=obs-local-only", ["obs-local-only"], False),
 ("env JWT_SECRET", "JWT_SECRET=0123456789abcdef0123456789abcdef0123 PORT=8080", ["0123456789abcdef"], False),
 ("env GRAFANA_ADMIN_PASSWORD lowercase", "grafana_admin_password: grafana-local-only", ["grafana-local-only"], False),
 ("uri userinfo", "DATABASE_URL=postgres://obs:s3cretpw@db:5432/obs?sslmode=disable", ["s3cretpw"], False),
 ("uri userinfo bare", "dial postgres://u:pw123@db:5432/x failed", ["pw123"], False),
 ("bearer", "Authorization: Bearer abc123.def456_ghi", ["abc123"], False),
 ("cookie header", "Cookie: session=deadbeef; csrf=cafebabe", ["deadbeef", "cafebabe"], False),
 ("set-cookie", "Set-Cookie: sid=feedface; HttpOnly", ["feedface"], False),
 ("jwt", "got eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig12345 here", ["eyJzdWIi"], False),
 ("json password with spaces", json.dumps({"level": "ERROR", "password": "correct horse battery", "n": 1}), ["correct horse battery"], True),
 ("json password with escaped quote", json.dumps({"password": 'pa"ss word', "x": "y"}), ["ss word"], True),
 ("json api_key + token", json.dumps({"api_key": "k-123", "Token": "t-456", "msg": "ok"}), ["k-123", "t-456"], True),
 ("json unquoted value", '{"password":12345,"msg":"ok"}', ["12345"], True),
 ("json authorization bearer", json.dumps({"Authorization": "Bearer abc.def"}), ["abc.def"], True),
 ("json database url", json.dumps({"url": "postgres://u:hunter2@db/x"}), ["hunter2"], True),
 ("escaped json password (loki)", json.dumps({"values": [["1", esc({"level": "ERROR", "password": "correct horse battery", "n": 1})]]}), ["correct horse battery"], True),
 ("escaped json secrets (loki)", json.dumps({"values": [["1", esc({"JWT_SECRET": "sss-111", "api_key": "kkk-222", "POSTGRES_PASSWORD": "ppp-333"})]]}), ["sss-111", "kkk-222", "ppp-333"], True),
 ("escaped json unquoted (loki)", json.dumps({"v": esc({"password": 12345})}), ["12345"], True),
 ("x-api-key header", "X-Api-Key: abcd1234efgh", ["abcd1234"], False),
 ("json X-Api-Key", json.dumps({"X-Api-Key": "abcd1234efgh", "ok": 1}), ["abcd1234"], True),
 ("authorization Token scheme", "Authorization: Token abcdef123456", ["abcdef123456"], False),
 ("proxy-authorization basic", "Proxy-Authorization: Basic dXNlcjpwdw==", ["dXNlcjpwdw"], False),
 ("pwd / pass / passphrase", "pwd=aaa111 pass=bbb222 passphrase=ccc333 DB_PASS=ddd444", ["aaa111", "bbb222", "ccc333", "ddd444"], False),
 ("credentials", "credentials=eee555 GOOGLE_CREDENTIALS: fff666", ["eee555", "fff666"], False),
 ("query params", "GET /x?key=k111&sig=s222&signature=g333&token=t444 HTTP/1.1", ["k111", "s222", "g333", "t444"], False),
 ("single quoted", "password='correct horse' secret='x y z'", ["correct horse", "x y z"], False),
 ("redis userinfo slash", "redis://:pa/ss/word@cache:6379/0", ["pa/ss/word"], False),
 ("pem block", "-----BEGIN RSA PRIVATE KEY-----\nMIIEabc123\nxyz789\n-----END RSA PRIVATE KEY-----", ["MIIEabc123", "xyz789"], False),
 ("pem block inside json (escaped newlines)", json.dumps({"msg": "-----BEGIN PRIVATE KEY-----\nQUJDREVGRw==\n-----END PRIVATE KEY-----"}), ["QUJDREVGRw"], True),
 ("json nested object under secret key", json.dumps({"credentials": {"user": "u", "pw": "nested-secret-1"}, "list": [1]}), ["nested-secret-1"], True),
 ("json array under token key", json.dumps({"tokens": ["tok-aaa", "tok-bbb"], "n": 2}), ["tok-aaa", "tok-bbb"], True),
 ("json pass/pwd keys", json.dumps({"pass": "p-1", "pwd": "p-2", "DB_PASS": "p-3", "compass": "keep"}), ["p-1", "p-2", "p-3"], True),
]
fails = 0
for name, inp, secrets, isjson in cases:
    out = red(inp); bad = [s for s in secrets if s in out]
    ok = not bad and "REDACTED" in out
    if isjson:
        try: json.loads(out)
        except Exception as e: ok = False; print("   invalid JSON:", e)
    print(("ok   " if ok else "FAIL ") + name + ("" if ok else "  leaked=%s out=%s" % (bad, out)))
    fails += not ok
# robustness: non-UTF-8 bytes, hostile long runs, timing
import time
r = subprocess.run(["python3", "-I", D + "/redact.py"], input=b"password=hunter2 \xff\xfe bad bytes", capture_output=True)
ok = r.returncode == 0 and b"hunter2" not in r.stdout and b"\xff\xfe" in r.stdout
print(("ok   " if ok else "FAIL ") + "non-UTF-8 input survives"); fails += not ok
for name, payload in [("64KB of name characters", "A" * 65536), ("64KB mixed name run", "aB3_.-" * 11000), ("many unclosed quotes", 'password="' * 6000), ("many BEGIN markers", "-----BEGIN PRIVATE KEY-----" * 2000)]:
    t0 = time.time(); out = red(payload); dt = time.time() - t0
    ok = dt < 2.0; print(("ok   " if ok else "FAIL ") + "%s finishes < 2s (%.2fs)" % (name, dt)); fails += not ok
# benign text must survive
benign = "level=info route=/repos/{owner}/{repo} status=200 msg=\"ok\""
ok = red(benign) == benign; print(("ok   " if ok else "FAIL ") + "benign text unchanged"); fails += not ok
print("redact tests: %d failed" % fails); sys.exit(1 if fails else 0)
PY
