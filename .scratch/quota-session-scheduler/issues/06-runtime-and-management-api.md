# 06: Runtime wiring, config v2, and management API

**What to build:** Registration with scheduler, across-priorities, usage plugin, and management API; config v2 keys with validation and backwards compatibility; management JSON routes (status, overview, sessions, activity, decision, usage, settings, settings/validate, refresh, simulate, sessions/clear, export) with redaction, size limits, rate limits, and confirmation for destructive actions.

**Blocked by:** 04, 05

**Status:** ready-for-agent

- [ ] Acceptance scenario passes again through the RPC seam (`scheduler.pick` + `usage.handle` JSON)
- [ ] Old configs (`mode`, `poll_interval`, `max_age`, `request_timeout` only) load unchanged
- [ ] No response contains tokens, API keys, raw session IDs, or credential paths (asserted over all routes)
- [ ] Panics in pick return "not handled"
