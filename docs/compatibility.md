# Compatibility matrix

What has actually been run against version 1.0.0 of the plugin, and how. "Pass" means the named test passed end to end. Everything else is marked as not run, with a reason.

All integration tests use synthetic credentials and local TLS fixtures that stand in for `api.anthropic.com` and `chatgpt.com`. Networking is disabled where the platform allows. No test contacts a real provider or spends real quota.

## CLIProxyAPI host

| CPA release | Platform | Test | Result |
|---|---|---|---|
| v8.0.27 (official release binary) | linux/arm64 | `make host-test` | Pass |
| v8.0.27 (official release binary) | linux/amd64 (emulated on an arm64 host) | `make host-test` | Pass |
| v8.0.27 (official Docker image `eceasy/cli-proxy-api:v8.0.27`) | linux/arm64 | `tests/docker_persistence.py` | Pass |
| v8.0.27 (official Docker image) | linux/amd64 | `tests/docker_persistence.py` | Runs in CI on `ubuntu-24.04`; not yet run for this branch |
| v8.0.27 (official release binary) | darwin/arm64 | `make load-test` | Pass |
| v8.0.27 | darwin/amd64, windows/amd64 | `make load-test` | Runs in CI; not yet run for this branch |
| v8.0.27 | darwin/arm64, darwin/amd64 | `make host-test` on macOS | Runs in CI; not yet run for this branch |
| v7.3.15 (official release binary) | linux/arm64 | `make host-test CPA_VERSION=7.3.15` | Pass, Claude only (see below) |
| v7.3.15 | linux/amd64 | `make host-test CPA_VERSION=7.3.15` | Not run |
| CPA Home dispatch (`home.enabled`) | — | — | Not supported: the host bypasses plugin schedulers |

`host-test` scenario steps (official binary, plugin loaded from `plugins.dir`):
- Claude and Codex accounts are discovered and their quota is polled.
- Shadow mode, then active mode.
- A session stays on its account when the ranking changes; a new session goes to the new first-ranked account.
- A 429 with exhausted-window headers rebinds the session.
- A 503 fails over for one request and keeps the binding; the account is used again after recovery.
- A Claude Code subagent inherits its parent session's account.
- Bindings survive a CPA restart.
- Streaming requests work.
- Settings are changed through CPA's `PATCH …/config` and the plugin reloads them.
- Dashboard assets are served, management routes require authentication, and no secrets appear in any response.
- Disabling the plugin returns routing to CPA.

`docker_persistence.py` covers routing and affinity, `docker restart`, and container removal and recreation with the state directory in the `plugins` bind mount. It also checks that the state file contains no secrets.

**Codex on v7.3.15:** CPA v7.3.15 talks to `chatgpt.com` over HTTP/2. The test fixture is a Python HTTP/1.1 server, so the Codex steps are skipped on v7 (`http2: frame too large`). This is a limitation of the fixture, not an observed plugin failure. The plugin's Codex code path is the same on v7 and v8 (same ABI, same schema). Treat Codex on v7 as untested.

## Plugin ABI

| Check | Platform | Result |
|---|---|---|
| `make native-test`: loads the release `.so` through CPA's exact C ABI with ctypes. Covers registration, capabilities, TLS quota reads, shadow/active routing, reset rollover, exhaustion, manual refresh, 500 picks, quiesce and resume, and buffer ownership. | linux/arm64 | Pass |
| same | linux/amd64 | Runs in CI; not yet run for this branch |
| Plugin ABI 1, schema version 6 | v7.3.15 and v8.0.27 SDKs | Identical; one binary loads in both |

## Builds

| Target | Built | Notes |
|---|---|---|
| linux_amd64 | yes (`file`: ELF x86-64) | pinned `golang:1.26-bookworm` image |
| linux_arm64 | yes (`file`: ELF aarch64) | pinned image |
| darwin_arm64 | yes | host Go 1.26.8 |
| darwin_amd64 | CI | patched Go toolchain (TSD slot 11), checked by `otool` |
| windows_amd64 | CI | MinGW-w64 10.0.0-3 in the pinned image |

The dashboard bundle (`web/` → `internal/mgmt/dist`) builds reproducibly: rebuilding from the lockfile produces byte-identical assets, and CI fails if the committed bundle is stale.

## Unit and component tests

| Suite | Result |
|---|---|
| `go test -race ./...` | Pass. Covers session extraction parity with CPA's `ExtractSessionInfo`, quota parsers and assessment, router policies, affinity, failover and rebind, persistence (atomic writes, corruption quarantine, lock), telemetry, management routes, a no-secrets check across every route and the state file, and concurrency. |
| `cd web && pnpm test` (Vitest) | Pass: 30 tests (formatting, durations, reasons, quota meter) |

## Clients

Session identity follows CPA's header precedence, and a unit test compares it with CPA's own extractor.

| Client | Headers used | Tested with |
|---|---|---|
| Claude Code | `X-Claude-Code-Session-Id`, `X-Claude-Code-Agent-Id`, `X-Claude-Code-Parent-Agent-Id` | synthetic requests in `host-test`, unit tests |
| Codex CLI | `Session-Id`, `Thread-Id`, `X-Codex-Turn-Metadata`, `X-Codex-Parent-Thread-Id`, `X-Openai-Subagent` | synthetic requests in `host-test`, unit tests |
| Generic clients | `X-Session-ID`, `X-Conversation-Id`, `X-Thread-Id`, … | unit tests |

Real Claude Code and Codex CLI binaries were not run against the proxy in this work.

## Dashboard

| Browser | Viewports | Themes | Result |
|---|---|---|---|
| Chromium (Playwright 1.64) | 1440×960, 390×844 (mobile emulation) | dark, light | all six pages render with no console errors (`web/scripts/screenshots.mjs`) |

Other browsers were not tested. The bundle targets current evergreen browsers and needs no polyfills.
