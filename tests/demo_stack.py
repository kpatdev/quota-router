"""Run CPA + the plugin + synthetic Claude/Codex upstreams with background traffic.

For dashboard development and screenshots only. Uses synthetic credentials,
synthetic keys, and local TLS fixtures; never contacts real providers.

    make demo            # serves http://127.0.0.1:18318 (management key: demo-management-key)
"""

import json
import os
from pathlib import Path
import random
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import http.server
import ssl
import urllib.error

sys.path.insert(0, str(Path(__file__).parent))
import host_smoke as hs  # noqa: E402

hs.CLAUDE = ["claude-work@example.invalid", "claude-personal@example.invalid", "claude-team-a@example.invalid", "claude-team-b@example.invalid"]
hs.CODEX = ["codex-main@example.invalid", "codex-backup@example.invalid", "codex-lab@example.invalid"]
hs.MGMT_KEY = "demo-management-key"
# Host.request bound its default key at import time.
hs.Host.request.__defaults__ = ("GET", None, hs.MGMT_KEY, None, False)


def main():
    assert os.environ.get("PLUGIN_ISOLATED_TEST") == "1"
    archive, library = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
    fx = hs.Fixture()
    quotas = {
        "claude-work@example.invalid": (62, 20), "claude-personal@example.invalid": (18, 96),
        "claude-team-a@example.invalid": (91, 40), "claude-team-b@example.invalid": (35, 130),
        "codex-main@example.invalid": (44, 30), "codex-backup@example.invalid": (8, 150), "codex-lab@example.invalid": (99, 6),
    }
    for a, (used, hours) in quotas.items():
        fx.quota[a] = [float(used), float(hours)]
    root = Path(tempfile.mkdtemp(prefix="qr-demo-"))
    binary = root / "cli-proxy-api"
    with tarfile.open(archive) as tar:
        member = next(m for m in tar if Path(m.name).name == "cli-proxy-api" and m.isfile())
        binary.write_bytes(tar.extractfile(member).read())
    binary.chmod(0o700)
    cert, key = root / "cert.pem", root / "key.pem"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-keyout", str(key), "-out", str(cert),
                    "-subj", "/CN=api.anthropic.com", "-addext", "subjectAltName=DNS:api.anthropic.com,DNS:chatgpt.com"], check=True, capture_output=True)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 443), hs.handler(fx))
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()

    auths = root / "auths"
    auths.mkdir()
    for a in quotas:
        claude = a.startswith("claude")
        token = ("sk-ant-oat-test-" if claude else "codex-token-") + a
        doc = {"type": "claude" if claude else "codex", "access_token": token, "account_uuid": a if claude else "",
               "account_id": "" if claude else a, "email": a, "expired": "2099-01-01T00:00:00Z", "disabled": False}
        if not claude:
            doc["plan_type"] = random.choice(["pro", "plus"])
        (auths / (a.split("@")[0] + "@example.invalid.json")).write_text(json.dumps(doc))
    plugins = root / "plugins"
    plugins.mkdir()
    (plugins / ("kpatdev-quota-router" + library.suffix)).write_bytes(library.read_bytes())
    (root / "config.yaml").write_text(f"""host: 0.0.0.0
port: 18318
auth-dir: {auths}
api-keys: [{hs.CLIENT_KEY}]
remote-management:
  allow-remote: true
  secret-key: {hs.MGMT_KEY}
  disable-control-panel: true
routing:
  strategy: fill-first
request-retry: 3
max-retry-interval: 1
plugins:
  enabled: true
  dir: {plugins}
  configs:
    kpatdev-quota-router:
      enabled: true
      priority: 10
      mode: active
      poll_interval: 1m
      max_age: 5m
      account_alias:
        claude-work@example.invalid.json: Work Max
      state_dir: {root / "state"}
""")
    host = hs.Host(binary, root, cert)
    host.start()
    host.wait(lambda: len(json.loads(host.request(hs.API + "/accounts"))["accounts"]) == len(quotas), "accounts", timeout=60)
    print("demo ready on :18318 — management key:", hs.MGMT_KEY, flush=True)

    sessions = [f"demo-{i}" for i in range(9)]
    rnd = random.Random(7)
    tick = 0
    while True:
        tick += 1
        try:
            s = rnd.choice(sessions)
            if rnd.random() < 0.65:
                agent = "agent-" + str(rnd.randint(1, 3)) if rnd.random() < 0.2 else None
                served = _call(lambda: _claude(host, s, agent))
                if served:
                    fx.quota[served][0] = min(99.5, fx.quota[served][0] + rnd.uniform(0.05, 0.25))
            else:
                served = _call(lambda: _codex(host, "cx-" + s))
                if served:
                    fx.quota[served][0] = min(99.5, fx.quota[served][0] + rnd.uniform(0.05, 0.2))
            if tick % 40 == 0:
                fx.fail_once.add(rnd.choice(list(quotas)))
            if tick % 150 == 0:
                fx.reject.add("claude-team-a@example.invalid")
            if tick % 150 == 20:
                fx.reject.discard("claude-team-a@example.invalid")
        except Exception as exc:  # keep the demo alive
            print("traffic error:", exc, flush=True)
        if host.proc.poll() is not None:
            host.dump()
            raise SystemExit("CPA exited")
        time.sleep(rnd.uniform(0.6, 2.0))


def _call(fn):
    try:
        return fn()
    except urllib.error.HTTPError:
        return None


def _claude(host, session, agent):
    headers = {"X-Claude-Code-Session-Id": session}
    if agent:
        headers["X-Claude-Code-Agent-Id"] = agent
    model = "claude-opus-4-6" if not agent else "claude-haiku-4-5-20251001"
    raw = host.request("/v1/messages", "POST", {"model": model, "max_tokens": 16, "messages": [{"role": "user", "content": "demo"}]}, hs.CLIENT_KEY, headers)
    return json.loads(raw)["content"][0]["text"]


def _codex(host, session):
    raw = host.request("/v1/responses", "POST", {"model": hs.CODEX_MODEL, "input": "demo", "stream": False}, hs.CLIENT_KEY, {"Session-Id": session})
    doc = json.loads(raw)
    return next(c["text"] for o in doc["output"] if o.get("type") == "message" for c in o["content"] if c.get("type") == "output_text")


if __name__ == "__main__":
    main()
