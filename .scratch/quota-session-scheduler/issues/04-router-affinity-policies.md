# 04: Router core: affinity, policies, failover, rebinding

**What to build:** The deep `Router` module: `Pick` chooses for new sessions by policy (reset-first, quota-pressure, balanced, manual-priority) after host priority, keeps established sessions on their account, inherits parent bindings, fails over temporarily without rebinding, rebinds on confirmed exhaustion/auth failure/removal/disable/model ineligibility per `rebind_policy`, delegates when quota is untrustworthy, and records explainable decisions. `Observe` ingests usage outcomes into account health (auth failure, exhaustion, circuit breaker) and learns bindings from delegated requests.

**Blocked by:** 02, 03

**Status:** ready-for-agent

- [ ] Brief §3 acceptance scenario passes for Claude and for Codex at the router seam
- [ ] Concurrent same-session picks bind once (race test)
- [ ] TTL expiry with activity renewal; LRU cap
- [ ] Separate providers, models, callers isolated
- [ ] Every decision lists eligible and skipped candidates with reasons
