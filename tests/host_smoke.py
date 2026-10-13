"""Run the official CPA release with the plugin against synthetic accounts and local TLS fixtures.

Covers the brief's acceptance scenario end to end through the real host: session affinity,
ranking changes, 429-driven failover and rebinding, 503 temporary failover, subagent
inheritance, restart persistence, Codex, streaming, settings via CPA's config API,
resources, and management authentication.
"""

import datetime as dt
import hashlib
import http.server
import json
import os
from pathlib import Path
import signal
import ssl
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request

PLUGIN = "kpatdev-quota-router"
API = "/v0/management/plugins/" + PLUGIN
CLAUDE_MODEL = "claude-opus-4-6"
CODEX_MODEL = "gpt-5.5"
CLAUDE = ["ca", "cb", "cc"]
CODEX = ["xa", "xb"]
CLIENT_KEY = "synthetic-client-key"
MGMT_KEY = "synthetic-management-key"


def now():
    return dt.datetime.now(dt.timezone.utc)


class Fixture:
    def __init__(self):
        self.lock = threading.Lock()
        # account → [weekly used %, weekly reset hours from fixture start]
        self.quota = {a: [20.0, 120.0] for a in CLAUDE + CODEX}
        self.start = now()
        self.served = []
        self.reject = set()  # 429 with provider headers saying the weekly window is rejected
        self.fail_once = set()  # one 503 then normal
        self.errors = []

    def reset_at(self, account):
        return self.start + dt.timedelta(hours=self.quota[account][1])

    def set(self, account, used=None, hours=None):
        with self.lock:
            if used is not None:
                self.quota[account][0] = used
            if hours is not None:
                self.quota[account][1] = (now() - self.start).total_seconds() / 3600 + hours

    def last(self):
        with self.lock:
            return self.served[-1] if self.served else None


def handler(fx):
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def account(self):
            auth = self.headers.get("Authorization", "")
            token = auth.removeprefix("Bearer sk-ant-oat-test-").removeprefix("Bearer codex-token-")
            if token not in fx.quota:
                fx.errors.append("unknown account " + auth[:30])
            return token

        def send(self, code, body, headers=None, content_type="application/json"):
            raw = body if isinstance(body, bytes) else json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(raw)))
            for k, v in (headers or {}).items():
                self.send_header(k, v)
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            account = self.account()
            used, _ = fx.quota[account]
            reset = fx.reset_at(account)
            if self.path == "/api/oauth/usage":
                return self.send(200, {
                    "seven_day": {"utilization": used, "resets_at": reset.isoformat()},
                    "five_hour": {"utilization": 0, "resets_at": None},
                })
            if self.path == "/backend-api/wham/usage":
                return self.send(200, {"plan_type": "pro", "rate_limit": {
                    "primary_window": {"used_percent": 0, "limit_window_seconds": 18000, "reset_after_seconds": 3600},
                    "secondary_window": {"used_percent": used, "limit_window_seconds": 604800,
                                         "reset_after_seconds": int((reset - now()).total_seconds())},
                }})
            fx.errors.append("unexpected GET " + self.path)
            self.send(404, {})

        def claude_headers(self, account, rejected=False):
            used, _ = fx.quota[account]
            reset = str(int(fx.reset_at(account).timestamp()))
            five = str(int((now() + dt.timedelta(hours=3)).timestamp()))
            return {
                "Anthropic-Ratelimit-Unified-5h-Utilization": "0.01",
                "Anthropic-Ratelimit-Unified-5h-Reset": five,
                "Anthropic-Ratelimit-Unified-5h-Status": "allowed",
                "Anthropic-Ratelimit-Unified-7d-Utilization": "1.0" if rejected else str(used / 100),
                "Anthropic-Ratelimit-Unified-7d-Reset": reset,
                "Anthropic-Ratelimit-Unified-7d-Status": "rejected" if rejected else "allowed",
                "Anthropic-Ratelimit-Unified-Status": "rejected" if rejected else "allowed",
            }

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])) or b"{}")
            account = self.account()
            path = self.path.split("?")[0]
            with fx.lock:
                if account in fx.fail_once:
                    fx.fail_once.discard(account)
                    return self.send(503, {"type": "error", "error": {"type": "overloaded_error", "message": "synthetic"}}, {"Retry-After": "1"})
                rejected = account in fx.reject
                if not rejected:
                    fx.served.append(account)
            if path == "/v1/messages":
                if rejected:
                    return self.send(429, {"type": "error", "error": {"type": "rate_limit_error", "message": "synthetic weekly limit"}},
                                     self.claude_headers(account, True))
                return self.claude_reply(account, body)
            if path == "/backend-api/codex/responses":
                if rejected:
                    return self.send(429, {"error": {"type": "usage_limit_reached", "message": "synthetic"}}, {
                        "X-Codex-Secondary-Used-Percent": "100", "X-Codex-Secondary-Window-Minutes": "10080",
                        "X-Codex-Secondary-Reset-After-Seconds": str(int((fx.reset_at(account) - now()).total_seconds())),
                    })
                done = {"type": "response.completed", "response": {
                    "id": "resp_1", "status": "completed", "model": CODEX_MODEL,
                    "output": [{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
                                "content": [{"type": "output_text", "text": account}]}],
                    "usage": {"input_tokens": 50, "input_tokens_details": {"cached_tokens": 40}, "output_tokens": 2, "total_tokens": 52}}}
                used, _ = fx.quota[account]
                return self.send(200, ("data: " + json.dumps(done) + "\n\n").encode(), {
                    "X-Codex-Secondary-Used-Percent": str(used), "X-Codex-Secondary-Window-Minutes": "10080",
                    "X-Codex-Secondary-Reset-After-Seconds": str(int((fx.reset_at(account) - now()).total_seconds())),
                }, "text/event-stream")
            fx.errors.append("unexpected POST " + self.path)
            self.send(404, {})

        def claude_reply(self, account, body):
            message = {"id": "msg_synthetic", "type": "message", "role": "assistant", "model": CLAUDE_MODEL,
                       "content": [{"type": "text", "text": account}], "stop_reason": "end_turn", "stop_sequence": None,
                       "usage": {"input_tokens": 3, "output_tokens": 1, "cache_read_input_tokens": 900, "cache_creation_input_tokens": 10}}
            headers = self.claude_headers(account)
            if not body.get("stream"):
                return self.send(200, message, headers)
            start = dict(message, content=[], stop_reason=None)
            events = [
                ("message_start", {"type": "message_start", "message": start}),
                ("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}),
                ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": account}}),
                ("content_block_stop", {"type": "content_block_stop", "index": 0}),
                ("message_delta", {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 1}}),
                ("message_stop", {"type": "message_stop"}),
            ]
            raw = "".join("event: " + e + "\ndata: " + json.dumps(p) + "\n\n" for e, p in events).encode()
            self.send(200, raw, headers, "text/event-stream")

        def log_message(self, *_):
            pass

    return Handler


class Host:
    def __init__(self, binary, root, cert):
        self.binary, self.root, self.cert = binary, root, cert
        self.proc = None
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.logs = []

    def start(self):
        log = (self.root / f"host-{len(self.logs)}.log").open("w+")
        self.logs.append(log)
        env = dict(os.environ, SSL_CERT_FILE=str(self.cert), GOMEMLIMIT="256MiB")
        self.proc = subprocess.Popen([str(self.binary), "--config", str(self.root / "config.yaml"), "--local-model"],
                                     cwd=self.root, env=env, stdout=log, stderr=subprocess.STDOUT)

    def stop(self):
        self.proc.send_signal(signal.SIGTERM)
        try:
            self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait()
            raise AssertionError("CPA shutdown timed out")

    def request(self, path, method="GET", body=None, key=MGMT_KEY, headers=None, raw=False):
        h = {"Content-Type": "application/json"}
        if key:
            h["Authorization"] = "Bearer " + key
        h.update(headers or {})
        req = urllib.request.Request("http://127.0.0.1:18318" + path, data=None if body is None else json.dumps(body).encode(), headers=h, method=method)
        with self.opener.open(req, timeout=20) as resp:
            data = resp.read()
            return (resp.status, dict(resp.headers), data) if raw else data

    def get(self, path):
        return json.loads(self.request(API + path))

    def wait(self, check, label, timeout=40):
        end, last = time.monotonic() + timeout, None
        while time.monotonic() < end:
            try:
                last = check()
                if last:
                    return last
            except (urllib.error.URLError, ValueError, KeyError) as exc:
                last = repr(exc)
            if self.proc.poll() is not None:
                raise AssertionError("CPA exited during " + label)
            time.sleep(0.25)
        raise AssertionError(label + ": " + repr(last)[:600])

    def dump(self):
        for log in self.logs:
            log.flush()
            log.seek(0)
            print(log.read()[-20000:], file=sys.stderr)


def main():
    assert os.environ.get("PLUGIN_ISOLATED_TEST") == "1", "Requires api.anthropic.com and chatgpt.com pinned to 127.0.0.1"
    if len(sys.argv) != 4:
        sys.exit("usage: host_smoke.py <cpa-archive> <cpa-checksums.txt> <plugin-library>")
    archive, checksums_path, library = (Path(a).resolve() for a in sys.argv[1:])
    checksums = dict(line.split()[::-1] for line in checksums_path.read_text().splitlines())
    assert hashlib.sha256(archive.read_bytes()).hexdigest() == checksums[archive.name], "CPA checksum mismatch"
    codex_supported = not archive.name.startswith("CLIProxyAPI_7.")
    fx = Fixture()
    fx.quota["ca"] = [20.0, 20.0]
    fx.quota["cb"] = [20.0, 96.0]
    fx.quota["cc"] = [20.0, 120.0]
    fx.quota["xa"] = [20.0, 20.0]
    fx.quota["xb"] = [20.0, 96.0]

    with tempfile.TemporaryDirectory(prefix="cpa-host-smoke-") as tmp:
        root = Path(tmp)
        binary = root / "cli-proxy-api"
        with tarfile.open(archive) as tar:
            member = next(m for m in tar if Path(m.name).name == "cli-proxy-api" and m.isfile())
            binary.write_bytes(tar.extractfile(member).read())
        binary.chmod(0o700)
        if sys.platform == "darwin":
            cert, key = Path(os.environ["FIXTURE_CERT"]), Path(os.environ["FIXTURE_KEY"])
        else:
            cert, key = root / "cert.pem", root / "key.pem"
            subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", str(key), "-out", str(cert),
                            "-subj", "/CN=api.anthropic.com", "-addext", "subjectAltName=DNS:api.anthropic.com,DNS:chatgpt.com"],
                           check=True, capture_output=True)
        bind = "0.0.0.0" if sys.platform == "darwin" else "127.0.0.1"
        server = http.server.ThreadingHTTPServer((bind, 443), handler(fx))
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert, key)
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        threading.Thread(target=server.serve_forever, daemon=True).start()

        auths = root / "auths"
        auths.mkdir()
        originals = {}
        for a in CLAUDE + CODEX:
            claude = a in CLAUDE
            doc = {"type": "claude" if claude else "codex",
                   "access_token": ("sk-ant-oat-test-" if claude else "codex-token-") + a,
                   "account_uuid": a if claude else "", "account_id": "" if claude else a,
                   "email": a + "@example.invalid", "expired": "2099-01-01T00:00:00Z",
                   "last_refresh": now().isoformat(), "disabled": False, "priority": 0}
            if not claude:
                doc["plan_type"] = "pro"
            (auths / (a + ".json")).write_text(json.dumps(doc))
            originals[a] = (auths / (a + ".json")).read_text()
        plugins = root / "plugins"
        plugins.mkdir()
        (plugins / (PLUGIN + library.suffix)).write_bytes(library.read_bytes())
        (root / "config.yaml").write_text(f"""host: 127.0.0.1
port: 18318
auth-dir: {auths}
api-keys: [{CLIENT_KEY}]
remote-management:
  allow-remote: false
  secret-key: {MGMT_KEY}
  disable-control-panel: true
routing:
  strategy: fill-first
request-retry: 3
max-retry-credentials: 0
max-retry-interval: 1
plugins:
  enabled: true
  dir: {plugins}
  configs:
    {PLUGIN}:
      enabled: true
      priority: 10
      mode: shadow
      state_dir: {root / "state"}
""")
        host = Host(binary, root, cert)

        def claude(session, agent=None, stream=False):
            headers = {"X-Claude-Code-Session-Id": session}
            if agent:
                headers["X-Claude-Code-Agent-Id"] = agent
            raw = host.request("/v1/messages", "POST", {"model": CLAUDE_MODEL, "max_tokens": 16, "stream": stream,
                                                        "messages": [{"role": "user", "content": "Synthetic routing test"}]},
                               CLIENT_KEY, headers)
            if stream:
                text = raw.decode()
                return next(a for a in CLAUDE if '"text":"' + a + '"' in text.replace(" ", ""))
            return json.loads(raw)["content"][0]["text"]

        def codex(session):
            raw = host.request("/v1/responses", "POST", {"model": CODEX_MODEL, "input": "Synthetic routing test", "stream": False},
                               CLIENT_KEY, {"Session-Id": session})
            doc = json.loads(raw)
            return next(c["text"] for o in doc["output"] if o.get("type") == "message" for c in o["content"] if c.get("type") == "output_text")

        def accounts():
            return {a["id"]: a for a in host.get("/accounts")["accounts"]}

        def quota_ready():
            acc = accounts()
            return len(acc) == 5 and all(a.get("last_refresh_at") for a in acc.values())

        def refresh(account_id, expect_hours):
            host.request(API + "/refresh", "POST", {"auth_id": account_id})
            want = fx.reset_at(account_id.removesuffix(".json"))

            def done():
                a = accounts()[account_id]
                w = next(w for w in a["windows"] if w["kind"] == "weekly")
                got = dt.datetime.fromisoformat(w["resets_at"].replace("Z", "+00:00"))
                return abs((got - want).total_seconds()) < 120
            host.wait(done, "refresh of " + account_id)

        def bound(provider):
            return {s["provider"] + ":" + s["auth_id"] for s in host.get("/sessions")["sessions"] if s["provider"] == provider}

        def generation():
            return host.get("/overview")["plugin"]["config_generation"]

        def configure(patch):
            before = generation()
            valid = json.loads(host.request(API + "/settings/validate", "POST", patch))
            assert valid["valid"], valid
            host.request(API + "/config", "PATCH", patch)
            host.wait(lambda: generation() == valid["generation"] != before, "plugin reload with " + json.dumps(patch))

        try:
            host.start()
            host.wait(quota_ready, "startup and quota discovery")
            ov = host.get("/overview")
            assert ov["plugin"]["version"] and ov["mode"] == "shadow", ov["plugin"]

            # Shadow: decisions are recorded, CPA keeps routing.
            claude("shadow-thread")
            decisions = host.get("/activity?provider=claude")["decisions"]
            assert decisions and not decisions[0]["applied"] and decisions[0]["auth_id"] == "ca.json", decisions[0]

            configure({"mode": "active"})

            # 1. A new thread selects A; follow-ups stay on A.
            assert claude("thread-1") == "ca"
            assert [claude("thread-1") for _ in range(3)] == ["ca"] * 3
            assert claude("thread-1", stream=True) == "ca", "streaming request left the thread's account"

            # 2. B becomes more attractive: the thread stays, a new thread takes B.
            fx.set("cb", hours=5)
            refresh("cb.json", 5)
            assert claude("thread-1") == "ca", "ranking change moved an established thread"
            assert claude("thread-2") == "cb", "new thread ignored the better account"

            # 3. A is exhausted (provider 429 with weekly rejected): the thread fails over and is rebound.
            fx.reject.add("ca")
            assert claude("thread-1") == "cb", "exhausted account did not fail over"
            fx.reject.discard("ca")
            host.wait(lambda: any(s["auth_id"] == "cb.json" and s["rebinds"] >= 1 for s in host.get("/sessions")["sessions"] if s["provider"] == "claude"),
                      "rebind of thread-1 to cb")
            assert claude("thread-1") == "cb", "thread returned to the exhausted account"

            # 4. Subagent inherits its parent's account even when another account ranks first.
            fx.set("cc", hours=1)
            refresh("cc.json", 1)
            assert claude("thread-3") == "cc", "new thread should prefer cc"
            assert claude("thread-1", agent="subagent-7") == "cb", "subagent did not inherit parent's account"

            # 5. A temporary 503 fails over for one request without moving the thread.
            fx.fail_once.add("cb")
            assert claude("thread-1") != "cb", "503 did not fail over"
            t1 = [s for s in host.get("/sessions")["sessions"] if s["provider"] == "claude" and s["failovers"] >= 1]
            assert t1 and t1[0]["auth_id"] == "cb.json", "temporary failure moved the binding: " + repr(t1)
            # CPA withholds cb until its own short cooldown lapses; then the thread returns.
            host.wait(lambda: claude("thread-1") == "cb", "return to bound account after CPA cooldown", timeout=90)

            # 6. Codex: same behavior, independent pool. CPA v7 forces HTTP/2 to chatgpt.com,
            # which this HTTP/1.1 fixture cannot serve, so Codex runs only on v8 hosts.
            if codex_supported:
                assert codex("codex-thread-1") == "xa"
                assert codex("codex-thread-1") == "xa"
                fx.reject.add("xa")
                assert codex("codex-thread-1") == "xb", "Codex exhaustion did not fail over"
                fx.reject.discard("xa")
                host.wait(lambda: "codex:xb.json" in bound("codex"), "Codex rebind")
                assert codex("codex-thread-1") == "xb"

            # 7. Restart: bindings survive.
            host.stop()
            state = (root / "state" / "state.json").read_text()
            for secret in ["sk-ant-oat", "codex-token", CLIENT_KEY, MGMT_KEY, "thread-1"]:
                assert secret not in state, "state file contains " + secret
            host.start()
            host.wait(quota_ready, "restart and quota discovery")
            assert claude("thread-1") == "cb", "Claude binding lost across restart"
            if codex_supported:
                assert codex("codex-thread-1") == "xb", "Codex binding lost across restart"

            # 8. Dashboard resources are public and data-free; the API requires the key.
            status, headers, body = host.request("/v0/resource/plugins/" + PLUGIN + "/index.html", key=None, raw=True)
            assert status == 200 and "default-src 'self'" in headers.get("Content-Security-Policy", ""), headers
            assert b"example.invalid" not in body
            try:
                host.request(API + "/overview", key=None)
                raise AssertionError("management API answered without a key")
            except urllib.error.HTTPError as exc:
                assert exc.code == 401, exc.code
            for path in ["/overview", "/sessions", "/activity", "/usage", "/export", "/settings"]:
                text = host.request(API + path).decode()
                for secret in ["sk-ant-oat", "codex-token", CLIENT_KEY, MGMT_KEY, "thread-1"]:
                    assert secret not in text, path + " leaked " + secret
            usage = host.get("/usage")
            assert usage["providers"]["claude"]["totals"]["cache_read_tokens"] > 0, "cache read tokens not recorded"

            # 9. Disabling the plugin hands routing back to CPA.
            host.request("/v0/management/plugins/" + PLUGIN + "/enabled", "PATCH", {"enabled": False})
            host.wait(lambda: claude("after-disable") == "ca", "fill-first after disable")

            # CPA rewrites auth files itself (refresh metadata); the plugin must never change
            # the fields that affect routing or the token.
            for a, text in originals.items():
                before, after = json.loads(text), json.loads((auths / (a + ".json")).read_text())
                for field in ("access_token", "priority", "disabled", "type"):
                    assert before.get(field) == after.get(field), f"credential {a} field {field} changed"
            assert not fx.errors, fx.errors
            print("PASS: CPA host routed " + ("Claude and Codex" if codex_supported else "Claude (Codex skipped: v7 HTTP/2 transport)") + " with session affinity, ranking-change stability, 429 rebind, "
                  "503 temporary failover, subagent inheritance, restart persistence, streaming, settings reload, "
                  "resources, auth, and disable rollback")
        except BaseException:
            host.dump()
            raise
        finally:
            if host.proc and host.proc.poll() is None:
                host.stop()
            for log in host.logs:
                log.close()
            server.shutdown()


if __name__ == "__main__":
    main()
