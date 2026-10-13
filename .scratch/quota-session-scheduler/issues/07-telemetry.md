# 07: Usage telemetry and burn-rate estimates

**What to build:** Bounded time series per provider/account (requests, failures, input/output tokens, cache read/creation, failovers, rebinds) from usage records and decisions, quota history samples, and burn-rate/depletion estimates with explicit insufficient-data states.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] Estimates require ≥ 2 samples in the same window spanning ≥ 15 minutes with rising usage; otherwise `insufficient_data`
- [ ] Window reset between samples never produces a negative rate
- [ ] Retention honors `history_retention`
