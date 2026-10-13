# 03: Quota model v2 with passive observation and smarter polling

**What to build:** Snapshots with explicit windows (five-hour, weekly, monthly, model-weekly by family) stored as used-percent 0–100, availability per model (`ready`, `exhausted`, `reset_pending`, `stale`, `unknown`), tolerant Claude/Codex poll parsers, passive parsers for `Anthropic-Ratelimit-Unified-*` (0–1 fractions) and `X-Codex-*` (percent) headers, and a poller with jitter, exponential backoff, post-reset polling, credential-change detection, and a rate-limited manual refresh.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Unit scaling is tested: Claude header 0.42 → 42% used; Codex 42 → 42% used; remaining computed in one place
- [ ] Passed reset → `reset_pending`, never `ready`, until fresh evidence
- [ ] Model-weekly windows only affect matching model families
- [ ] Parsers fuzzed; unknown fields ignored; missing required long window → unknown
- [ ] Poll failures back off with jitter; 401/403 mark auth suspect without discarding history
