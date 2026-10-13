"""Synthetic Claude/Codex upstream for container tests, with a control endpoint.

Serves the same fixture as host_smoke.py on :443 (TLS, cert/key from argv) and
accepts POST /__control {"account": ..., "used": ..., "hours": ...,
"reject": bool} to change quota between test steps.
"""

import http.server
import json
from pathlib import Path
import ssl
import sys

sys.path.insert(0, str(Path(__file__).parent))
import host_smoke as hs  # noqa: E402


def main():
    cert, key, accounts = sys.argv[1], sys.argv[2], sys.argv[3].split(",")
    fx = hs.Fixture()
    for a in accounts:
        fx.quota[a] = [20.0, 120.0]
    base = hs.handler(fx)

    class Handler(base):
        def do_POST(self):
            if self.path == "/__control":
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                fx.set(body["account"], body.get("used"), body.get("hours"))
                if "reject" in body:
                    (fx.reject.add if body["reject"] else fx.reject.discard)(body["account"])
                return self.send(200, {"ok": True})
            return super().do_POST()

        def do_GET(self):
            if self.path == "/__served":
                return self.send(200, {"served": fx.served[-20:]})
            return super().do_GET()

    server = http.server.ThreadingHTTPServer(("0.0.0.0", 443), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    print("fixture ready", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
