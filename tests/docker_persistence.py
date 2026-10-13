"""Verify session bindings survive container restart and recreation with the official CPA image.

Runs on a Docker host:
  python3 tests/docker_persistence.py <plugin.so> [image]

A fixture container plays api.anthropic.com/chatgpt.com on a private network; the CPA
container mounts config, auths, and plugins (including the plugin state directory) as
bind mounts, exactly like deploy/docker-compose.yml. Synthetic credentials only.
"""

import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
PLUGIN = "kpatdev-quota-router"
API = "/v0/management/plugins/" + PLUGIN
MGMT, CLIENT = "persist-management-key", "persist-client-key"
GO_IMAGE = "golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d"
ACCOUNTS = ["ca", "cb"]


def sh(*args, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True).stdout.strip()


def main():
    library = Path(sys.argv[1]).resolve()
    image = sys.argv[2] if len(sys.argv) > 2 else "eceasy/cli-proxy-api:v8.0.27"
    arch = "linux/arm64" if "arm64" in str(library) else "linux/amd64"
    tag = f"qrp{os.getpid()}"
    tmp = Path(tempfile.mkdtemp(prefix="qr-persist-"))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))

    def http(url, method="GET", body=None, key=MGMT, headers=None):
        h = {"Content-Type": "application/json", "Authorization": "Bearer " + key, **(headers or {})}
        req = urllib.request.Request(url, data=None if body is None else json.dumps(body).encode(), headers=h, method=method)
        with opener.open(req, timeout=20) as r:
            return json.loads(r.read() or b"{}")

    cpa = lambda path, **kw: http("http://127.0.0.1:18319" + path, **kw)  # noqa: E731
    control = lambda **body: http("https://127.0.0.1:18443/__control", "POST", body)  # noqa: E731

    def wait(check, label, timeout=90):
        end, last = time.time() + timeout, None
        while time.time() < end:
            try:
                last = check()
                if last:
                    return last
            except (urllib.error.URLError, ConnectionError, ValueError, KeyError) as e:
                last = repr(e)
            time.sleep(0.5)
        print(sh("docker", "logs", "--tail", "80", tag + "-cpa", check=False), file=sys.stderr)
        raise AssertionError(label + ": " + str(last)[:400])

    def claude(session):
        r = cpa("/v1/messages", method="POST", key=CLIENT, headers={"X-Claude-Code-Session-Id": session},
                body={"model": "claude-opus-4-6", "max_tokens": 8, "messages": [{"role": "user", "content": "hi"}]})
        return r["content"][0]["text"]

    def ready():
        acc = cpa(API + "/accounts")["accounts"]
        return len(acc) == 2 and all(a.get("last_refresh_at") for a in acc)

    def start_cpa():
        sh("docker", "run", "-d", "--name", tag + "-cpa", "--platform", arch, "--network", tag,
           "--add-host", f"api.anthropic.com:{fixture_ip}", "--add-host", f"chatgpt.com:{fixture_ip}",
           "-e", "SSL_CERT_FILE=/certs/cert.pem", "-p", "127.0.0.1:18319:8317",
           "-v", f"{tmp}/certs:/certs:ro", "-v", f"{tmp}/config.yaml:/CLIProxyAPI/config.yaml",
           "-v", f"{tmp}/auths:/root/.cli-proxy-api", "-v", f"{tmp}/plugins:/CLIProxyAPI/plugins", image)

    try:
        (tmp / "certs").mkdir()
        sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", str(tmp / "certs/key.pem"),
           "-out", str(tmp / "certs/cert.pem"), "-subj", "/CN=api.anthropic.com", "-addext", "subjectAltName=DNS:api.anthropic.com,DNS:chatgpt.com")
        os.chmod(tmp / "certs/key.pem", 0o644)
        (tmp / "auths").mkdir()
        for a in ACCOUNTS:
            (tmp / "auths" / f"{a}.json").write_text(json.dumps({"type": "claude", "access_token": "sk-ant-oat-test-" + a, "account_uuid": a,
                                                                  "email": a + "@example.invalid", "expired": "2099-01-01T00:00:00Z"}))
        (tmp / "plugins").mkdir()
        shutil.copy(library, tmp / "plugins" / f"{PLUGIN}.so")
        (tmp / "config.yaml").write_text(f"""host: ""
port: 8317
auth-dir: /root/.cli-proxy-api
api-keys: [{CLIENT}]
remote-management:
  allow-remote: true
  secret-key: {MGMT}
  disable-control-panel: true
routing:
  strategy: fill-first
request-retry: 2
plugins:
  enabled: true
  dir: plugins
  configs:
    {PLUGIN}:
      enabled: true
      priority: 10
      mode: active
""")
        sh("docker", "network", "create", tag)
        sh("docker", "run", "-d", "--name", tag + "-fx", "--platform", arch, "--network", tag, "-p", "127.0.0.1:18443:443",
           "-v", f"{ROOT}:/src:ro", "-v", f"{tmp}/certs:/certs:ro", "-w", "/src", GO_IMAGE,
           "python3", "-u", "tests/fixture_server.py", "/certs/cert.pem", "/certs/key.pem", ",".join(ACCOUNTS))
        fixture_ip = sh("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", tag + "-fx")
        wait(lambda: control(account="ca", hours=20), "fixture")
        control(account="cb", hours=96)
        start_cpa()
        wait(ready, "CPA and quota discovery")

        assert claude("persist-1") == "ca"
        control(account="cb", hours=5)
        cpa(API + "/refresh", method="POST", body={"auth_id": "cb.json"})
        wait(lambda: any(a["id"] == "cb.json" and a["next_reset"] and a["rank"] == 1 for a in cpa(API + "/accounts")["accounts"]), "cb ranks first")
        assert claude("persist-1") == "ca", "ranking change moved the bound session"
        assert claude("persist-2") == "cb"

        sh("docker", "restart", "-t", "20", tag + "-cpa")
        wait(ready, "restart")
        assert claude("persist-1") == "ca", "binding lost across docker restart"
        assert claude("persist-2") == "cb", "binding lost across docker restart"

        # Recreate the container from scratch (upgrade path): state lives in the plugins mount.
        sh("docker", "rm", "-f", tag + "-cpa")
        start_cpa()
        wait(ready, "recreate")
        assert claude("persist-1") == "ca", "binding lost across container recreation"
        # The plugin writes state owner-only (0600) as the container's root user, so read it in the container.
        state = sh("docker", "exec", tag + "-cpa", "cat", f"/CLIProxyAPI/plugins/{PLUGIN}-data/state.json")
        assert '"bindings"' in state, "state file has no bindings"
        assert "sk-ant-oat" not in state and "persist-1" not in state and MGMT not in state, "state file contains secrets"
        print(f"PASS: bindings survived docker restart and container recreation ({image}, {arch}); state file holds no secrets")
    finally:
        # Hand root-owned files written by the container back to us so the temp dir can be removed.
        sh("docker", "exec", tag + "-cpa", "chown", "-R", f"{os.getuid()}:{os.getgid()}", "/CLIProxyAPI/plugins", "/root/.cli-proxy-api", check=False)
        for c in (tag + "-cpa", tag + "-fx"):
            sh("docker", "rm", "-f", c, check=False)
        sh("docker", "network", "rm", tag, check=False)
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
