#!/usr/bin/env python3
"""redact.py : stdin -> stdout, secrets replaced by [REDACTED]. Stdlib only; run with `python3 -I`.

Input is read as bytes (surrogateescape, never fails on odd encodings). Two modes:
 * JSON input: the parsed structure is walked. Any key that looks secret (password, passwd, passphrase, pass, pwd,
   secret, token, api_key, authorization, session, cookie, credential, signature, private_key) gets the value
   "[REDACTED]" whatever its type (string, number, nested object/array). Every string value goes through the text rules,
   and a string that itself holds JSON (Loki log lines) is walked recursively. The output is always valid JSON.
 * Other text: regex rules (case-insensitive): KEY=value / KEY: value (double, single and escaped quotes, spaces ok),
   whole Authorization / Proxy-Authorization / Cookie / Set-Cookie header values, Bearer/Basic, URI userinfo
   (scheme://user:pw@host, passwords may contain "/"), ?key= ?sig= ?signature= ?token= style query parameters, PEM private
   key blocks, JWTs.
All quantifiers are bounded and a SIGALRM watchdog (REDACT_TIMEOUT, default 20 s) aborts with exit 3 and no output, so a
hostile input cannot hang the pipeline. Callers must treat a non-zero exit as "drop the content".
"""
import json, os, re, signal, sys

R = "[REDACTED]"
WORDS = r"(?:PASSWORD|PASSWD|PASSPHRASE|SECRET|TOKEN|API[_-]?KEY|CREDENTIALS?|AUTHORIZATION|SESSION|COOKIE|SIGNATURE|PRIVATE[_-]?KEY)"
# Names start at a run boundary, so scanning a long run of name characters is linear, not quadratic.
NAME = r"(?<![A-Z0-9_.-])(?:[A-Z0-9_.-]{0,40}" + WORDS + r"[A-Z0-9_.-]{0,40}|(?:[A-Z0-9]{1,20}[_.-])?(?:PASS|PWD)(?![A-Z0-9]))"
KV = re.compile(
    r"(?P<kq1>\\*[\"']?)(?P<name>" + NAME + r")(?P<kq2>\\*[\"']?)(?P<sep>[ \t]*[=:][ \t]*)"
    r"(?:"
    r"(?P<q>\\+\")(?P<v1>(?:(?!(?P=q)).){0,4096})(?P=q)"      # escaped-quote form: \"value\"
    r"|\"(?P<v2>(?:[^\"\\]|\\.){0,4096})\""                    # double-quoted, spaces and escapes ok
    r"|'(?P<v4>[^'\n]{0,4096})'"                               # single-quoted
    r"|(?P<v3>(?!\[REDACTED)[^\s\"'\\&,;}\]]{1,4096})"         # unquoted token
    r")", re.I)
HEADER = re.compile(r"(?i)\b((?:proxy-)?authorization|(?:set-)?cookie)([ \t]*[:=][ \t]*)(?![\"\\\[])[^\r\n\"\\}]{1,8192}")
USERINFO = re.compile(r"(://[^:/@\s\"'\\]{0,256}:)[^@\s\"'\\]{1,1024}(@)")
BEARER = re.compile(r"(?i)\b(Bearer|Basic)(\s+)[A-Za-z0-9._~+/=-]{1,4096}")
JWT = re.compile(r"\beyJ[A-Za-z0-9_-]{8,4096}\.[A-Za-z0-9_-]{8,4096}\.[A-Za-z0-9_-]{0,4096}")
QUERY = re.compile(r"(?i)([?&](?:key|sig|signature|token|access[_-]?token|api[_-]?key|apikey|secret|password|passwd|auth)=)[^&\s\"'\\#]{1,2048}")
PEM = re.compile(r"-----BEGIN [A-Z ]{0,40}PRIVATE KEY-----.{0,16384}?(?:-----END [A-Z ]{0,40}PRIVATE KEY-----|\Z)", re.S)
SECRET_KEY = re.compile(r"(?i)password|passwd|passphrase|secret|token|api[_-]?key|apikey|authorization|session|cookie|credential|signature|private[_-]?key|(?:^|[_.-])(?:pass|pwd)(?:$|[_.-])")


def _kv(m):
    head = m.group("kq1") + m.group("name") + m.group("kq2") + m.group("sep")
    if m.group("q") is not None:
        return head + m.group("q") + R + m.group("q")
    if m.group("v2") is not None:
        return head + '"' + R + '"'
    if m.group("v4") is not None:
        return head + "'" + R + "'"
    quote = m.group("kq2")
    if quote:                       # JSON-ish context: keep it syntactically valid
        return head + quote + R + quote
    return head + R


def text(t):
    t = PEM.sub(R, t)
    t = BEARER.sub(lambda m: m.group(1) + m.group(2) + R, t)
    t = USERINFO.sub(lambda m: m.group(1) + R + m.group(2), t)
    t = HEADER.sub(lambda m: m.group(1) + m.group(2) + R, t)
    t = KV.sub(_kv, t)
    t = QUERY.sub(lambda m: m.group(1) + R, t)
    return JWT.sub("[REDACTED_JWT]", t)


def walk(o):
    if isinstance(o, dict):
        return {k: (R if SECRET_KEY.search(str(k)) else walk(v)) for k, v in o.items()}
    if isinstance(o, list):
        return [walk(v) for v in o]
    if isinstance(o, str):
        s = o.lstrip()
        if s[:1] in "{[" and len(s) > 1:      # a log line that is itself JSON
            try:
                return json.dumps(walk(json.loads(o)), separators=(",", ":"), ensure_ascii=False)
            except ValueError:
                pass
        return text(o)
    return o


def redact(t):
    try:
        o = json.loads(t)
    except ValueError:
        o = None
    if isinstance(o, (dict, list)):
        return json.dumps(walk(o), separators=(",", ":"), ensure_ascii=False)
    return text(t)


def _alarm(*_):
    os._exit(3)


if __name__ == "__main__":
    try:
        signal.signal(signal.SIGALRM, _alarm); signal.alarm(int(os.environ.get("REDACT_TIMEOUT", "20")))
    except (AttributeError, ValueError):
        pass
    data = sys.stdin.buffer.read().decode("utf-8", errors="surrogateescape")
    out = redact(data)
    sys.stdout.buffer.write(out.encode("utf-8", errors="surrogateescape"))
