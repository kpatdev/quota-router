# 02: Codex windows of unrecognized length

**What to build:** An unrecognized `limit_window_seconds` must not fail the whole poll (`invalid_codex_window`, codex.go:63). Classify by length: 18000s five-hour, 604800s weekly, 28-31 days monthly; anything else is kept out of availability with a debug log and the rest of the payload is used. When no length is given, default primary to five-hour and secondary to weekly (poll and headers). Passive `X-Codex-*-Window-Minutes` uses the same classification. Fix docs/limitations.md to match.

**Blocked by:** none

**Status: done** - Unknown lengths are skipped with debug logging and missing lengths default by slot in polling and headers.

- [x] Unknown lengths are skipped, not fatal
- [x] Missing lengths default by position
- [x] Tests for both
