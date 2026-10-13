# Glossary

**Account** — one CPA credential (auth file) for Claude or Codex, identified by its CPA auth ID. Never "key" or "token".

**Candidate** — an account CPA offers in a `scheduler.pick` request. Only candidates can be chosen.

**Session** — one conversation thread as identified by client headers (Claude Code session/agent, Codex session/thread). Requests without one are *sessionless*.

**Session key** — the SHA-256 hash of caller scope and session ID. The only form stored or shown.

**Binding** — the association (provider, model scope, session key) → account, with TTL renewed by activity.

**Affinity** — choosing a session's bound account because it is bound.

**Failover** — serving one request from another account while the binding stays unchanged.

**Rebind** — replacing a binding's account because the bound account is permanently unusable for it.

**Inheritance** — a child session (subagent, fork) taking its parent's account on its first request.

**Quota window** — one provider limit with its own semantics: five-hour, weekly, monthly, or model-weekly (for a model family).

**Snapshot** — the latest windows for an account, with source (poll or passive) and observation time.

**Availability** — the classification of an account for a model: `ready`, `exhausted`, `reset_pending`, `stale`, `unknown`. In code: `quota.State`; in the API: `quota_state`.

**Usable** — offered, not disabled, not auth-failed, not exhausted for the model, circuit not open.

**Policy** — the ranking for new sessions: reset-first, quota-pressure, balanced, manual-priority. The config key is `strategy` (kept for compatibility); `rebind_policy` is unrelated and governs when a session moves.

**Decision** — the record of one pick: outcome, chosen account, ranked and skipped candidates with reasons.

**Shadow mode** — decisions are recorded but CPA chooses; used to compare proposed and actual routing.

**Delegate** — return "not handled" so CPA's built-in scheduler chooses.
