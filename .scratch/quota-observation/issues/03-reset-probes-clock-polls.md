# 03: Reset probes, clock-aligned polls, passive skip

**What to build:**
- Regular polls align to wall-clock multiples of `poll_interval` (truncate+add), replacing interval-with-jitter. Keep error backoff and the 3-poll concurrency cap; spread accounts within a slot with a small deterministic offset so they don't burst.
- For every known future reset, schedule a probe at reset-30s and reset+30s. Skip a probe if that window was observed at or after (probe time - 10s), or the account was polled within 10s. The post-reset probe is only eligible until reset+10m. This replaces the current reset+5s poll.
- Passive skip: skip a scheduled (not probe, not manual) poll if a passive observation for the account arrived within `passive_skip` (default 60s, 0 disables) and the last successful poll is younger than `passive_skip_max` (default 30m).
- Add config keys with validation and document them.

**Blocked by:** none

**Status:** done

Implemented clock-aligned polls, reset probes, bounded passive skip, config validation and docs; all requested Go checks pass.

- [x] Fake-clock tests for alignment, both probes, probe suppression, passive skip and its cap
- [x] Config reference updated
