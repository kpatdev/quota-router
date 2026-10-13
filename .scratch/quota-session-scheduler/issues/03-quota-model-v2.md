# 03: Quota model v2 with passive observation and smarter polling

**What to build:** Snapshots with explicit windows (five-hour, weekly, monthly, model-weekly by family) stored as used-percent 0–100, availability per model (`ready`, `exhausted`, `reset_pending`, `stale`, `unknown`), tolerant Claude/Codex poll parsers, passive parsers for `Anthropic-Ratelimit-Unified-*` (0–1 fractions) and `X-Codex-*` (percent) headers, and a poller with jitter, exponential backoff, post-reset polling, credential-change detection, and a rate-limited manual refresh.

**Blocked by:** 01

**Status:** done

Done. Fuzzers: FuzzParseClaudeUsage, FuzzParseCodexUsage. Full window without a reset time is exhausted (review fix).

- [x] Unit scaling is tested: Claude header 0.42 → 42% used; Codex 42 → 42% used; remaining computed in one place
- [x] Passed reset → `reset_pending`, never `ready`, until fresh evidence
- [x] Model-weekly windows only affect matching model families
- [x] Parsers fuzzed; unknown fields ignored; missing required long window → unknown
- [x] Poll failures back off with jitter; 401/403 mark auth suspect without discarding history
