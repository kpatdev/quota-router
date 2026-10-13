# 02: Session identity from request headers

**What to build:** A pure extractor that turns scheduler-pick headers and metadata into an optional session reference (hashed key, hashed parent key, client kind, agent kind) using CPA's header precedence for Claude Code, Codex, generic headers, and WebSocket execution sessions; caller scope isolates tenants; per-request IDs are never used.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Claude Code main, subagent (agent/parent-agent headers) and Codex main, thread, subagent, fork cases produce CPA-equivalent IDs and parents
- [ ] `X-Client-Request-Id`, `Idempotency-Key`, and request IDs never produce a session
- [ ] Parity test against CPA v8 `session.ExtractSessionInfo(headers, nil, metadata)` on a fixture corpus
- [ ] Raw session IDs never leave the extractor except hashed
