# Spec: quota-aware, session-aware scheduler for Claude and Codex

Status: ready-for-agent. Architecture and host facts: [`docs/architecture.md`](../architecture.md).
Tickets: [`.scratch/quota-session-scheduler/issues/`](../../.scratch/quota-session-scheduler/issues/).

## Problem Statement

An operator runs CLIProxyAPI (CPA) with several Claude and Codex subscription
accounts and points Claude Code and Codex at it. The existing kpatdev-quota-router
plugin picks the account whose weekly quota resets soonest **on every request**.
Agent threads therefore hop between accounts mid-conversation, which throws away
the provider-side prompt cache, makes cost and latency unpredictable, and hides
which account a thread is using. When an account is exhausted or loses its
login, the operator has no view of what happened, which sessions moved, or what
the scheduler will do next. The only interface is a raw JSON status endpoint.

## Solution

A drop-in replacement for the plugin (same plugin ID and config keys) that:

1. Picks an account for each **new** session by a configurable quota policy
   (reset-first by default) and then keeps that session on the same account
   while it stays usable.
2. Distinguishes temporary failures (fail over this request, keep the binding)
   from confirmed exhaustion, auth failure, and removal (rebind), and never
   moves a healthy session because rankings changed.
3. Lets subagents inherit their parent's account.
4. Survives restarts with a small atomic state file.
5. Observes quota passively from real responses in addition to polling.
6. Serves a dense, dark-by-default dashboard inside CPA's plugin resources that
   shows accounts, quota windows, bound sessions, routing decisions, usage and
   cache metrics, a "next to use" panel, and a policy simulator, and edits
   settings through CPA's config API.

## User Stories

1. As an operator, I want a new Claude Code thread to start on the account whose usable quota expires soonest, so that quota is not wasted.
2. As an operator, I want every later request of that thread to use the same account, so that prompt caching keeps working.
3. As an operator, I want a thread to stay put when another account becomes more attractive, so that rankings never break caching.
4. As an operator, I want a new thread to pick the newly attractive account, so that new work still follows quota.
5. As an operator, I want a thread whose account runs out of quota to move to another account and stay there, so that work continues.
6. As an operator, I want a 5xx or timeout to retry on another account without moving the thread, so that one blip does not cost the cache.
7. As an operator, I want an account that recovers not to steal threads back, so that moved threads keep their new cache.
8. As an operator, I want a subagent to use its parent's account when I enable inheritance, so that the parent/child pair shares limits and cache.
9. As an operator, I want to turn inheritance off, so that subagents spread across accounts when I prefer throughput.
10. As an operator, I want bindings to survive a plugin reload and a container restart, so that a deploy does not reshuffle every thread.
11. As an operator, I want idle bindings to expire after a configurable TTL renewed by activity, so that the table does not grow forever.
12. As an operator, I want two simultaneous first requests of one thread to land on one account, so that a race does not split a thread.
13. As an operator, I want Claude and Codex pools handled independently, so that one provider's state never affects the other.
14. As an operator, I want bindings scoped per model by default (optionally per provider), so that models with separate limits are scheduled independently.
15. As an operator, I want threads of different API-key callers isolated even if their session IDs collide, so that tenants cannot affect each other.
16. As an operator, I want requests without a trustworthy session ID to get quota-aware selection without affinity, so that nothing is pinned by guesswork.
17. As an operator, I want CPA's priority tiers, disabled flags, cooldowns, and model eligibility respected, so that the plugin never overrides host safety.
18. As an operator, I want to choose reset-first, quota-pressure, balanced, or manual-priority policies, so that I can match my usage pattern.
19. As an operator, I want policy changes to affect only new sessions and permitted failovers, so that switching policy does not reshuffle live threads.
20. As an operator, I want the scheduler to defer to CPA when it has no trustworthy quota data, so that it never routes on guesses.
21. As an operator, I want five-hour, weekly, monthly, and model-specific windows treated with their own semantics, so that a Sonnet limit does not block Opus.
22. As an operator, I want an exhausted window to stay exhausted until fresh data proves the reset, so that a passed timestamp alone never re-enables an account.
23. As an operator, I want quota to refresh on clock-aligned intervals with error backoff, and before and after each expected reset, so that data stays fresh without hammering endpoints.
24. As an operator, I want quota observed from real response headers, so that busy accounts stay fresh between polls.
25. As an operator, I want a rate-limited manual refresh, so that I can check an account on demand.
26. As an operator, I want 401/403 to park an account until it is re-logged-in and verified, so that a dead login stops receiving traffic.
27. As an operator, I want repeated transient failures to open a short circuit breaker, so that a flapping account is avoided temporarily.
28. As an operator, I want removed or replaced credentials detected, so that bindings and cached quota never follow a changed identity.
29. As an operator, I want the plugin to fall back to CPA's scheduler on any internal failure, so that the proxy keeps working.
30. As an operator, I want shadow mode to record proposed versus actual routing without changing traffic, so that I can validate before enabling.
31. As an operator, I want a dashboard overview with account counts, health, active sessions, upcoming resets, last refresh, policy, and plugin health, so that I see the system state at a glance.
32. As an operator, I want Claude and Codex account lists with five-hour, weekly, and model quota bars, reset times, countdowns, refresh age, bound sessions, routing status, and "preferred next", so that I can compare accounts quickly.
33. As an operator, I want bars to show remaining capacity consistently regardless of whether the provider reports utilization or remaining, so that I never misread a full account as empty.
34. As an operator, I want to filter by provider, search, and sort by reset time, availability, remaining quota, and session count, so that I can find accounts fast.
35. As an operator, I want accounts whose usable capacity expires soon highlighted, so that I can see what will be wasted.
36. As an operator, I want a "next to use" panel per provider with the ranking reason, so that I know where new threads go.
37. As an operator, I want burn-rate estimates and projected depletion times, labeled as estimates with insufficient-data states, so that I can anticipate exhaustion.
38. As an operator, I want a sessions view with redacted IDs, provider, model, account, age, last activity, and failover history, so that I can verify affinity.
39. As an operator, I want a routing decision explorer showing chosen account, reason, eligible and skipped accounts with reasons, affinity and failover flags, so that I can audit any decision.
40. As an operator, I want requests, tokens, cache read/write, failures, and account switches over time, using only metrics CPA exposes, so that I can see cache efficiency.
41. As an operator, I want to simulate each policy for a new session without applying it, so that I can choose a policy with evidence.
42. As an operator, I want to change policy, TTL, inheritance, and other settings from the dashboard with validation and a clear description of the effect, so that I do not edit YAML by hand.
43. As an operator, I want clearing bindings to require confirmation, so that I cannot reset all threads by accident.
44. As an operator, I want to export redacted diagnostics, so that I can share a problem report safely.
45. As an operator, I want aliases for accounts, so that rows are recognizable without exposing emails.
46. As an operator, I want the dashboard to work on desktop, tablet, and phone in dark and light mode with keyboard access, so that I can check it anywhere.
47. As an operator, I want the management key kept in memory only, so that it is not left in browser storage.
48. As an operator, I want no tokens, credential files, prompts, API keys, or raw session IDs in the dashboard, logs, exports, or state file, so that the plugin cannot leak secrets.
49. As an operator, I want a Docker Compose example with a persistent state volume and an upgrade path from the original plugin, so that I can deploy safely.
50. As an operator, I want one plugin binary that loads in CPA v7.3.15 and v8, so that I can upgrade CPA independently.

## Implementation Decisions

**Modules.** The plugin is split into deep modules behind small interfaces:

- *Session identity*: pure function from request headers and metadata to an
  optional session reference (hashed key, parent key, client kind, agent kind).
  Mirrors CPA's header precedence; excludes per-request IDs.
- *Quota*: snapshot model (windows with kind, model family, used percent,
  reset time, source, observed time), provider parsers for poll payloads and
  passive headers, availability classification per model (`ready`, `exhausted`,
  `reset_pending`, `stale`, `unknown`), and the background poller.
- *Router*: the core. `Pick(request) → Decision` and `Observe(usage outcome)`.
  Owns bindings, account health, policies, and the decision log. Pure with
  respect to time (injected clock) and I/O (no network, no disk). This is the
  primary test seam; the acceptance scenario runs here and again through the
  RPC seam.
- *State store*: load/save of a versioned JSON document with atomic writes,
  debounce, corruption quarantine, and an advisory lock.
- *Telemetry*: bounded time-series of usage (requests, tokens, cache, failures,
  switches) and quota history samples for burn-rate estimates.
- *Management*: JSON handlers (overview, accounts, sessions, activity, decision
  detail, usage, settings schema/validate, refresh, simulate, clear sessions,
  export) and embedded static dashboard assets.
- *Runtime*: RPC dispatch, lifecycle (register/reconfigure keeps state;
  quiesce/shutdown flushes), host callbacks.

**RPC contract.** Registration declares `scheduler`, `scheduler_across_priorities`
(config, default true), `usage_plugin`, `management_api`; schema version 6.
`scheduler.pick` must never block on I/O and must never panic out (panics
return "not handled").

**Session key.** `sha256(provider-independent caller_scope + "\x00" + session ID)`;
binding key = (provider, model scope, session key). Model scope is the base
model name (thinking suffix removed) or the provider, per `affinity_scope`.

**Decision outcomes.** `affinity`, `inherited`, `new_session`, `no_session`,
`failover` (temporary, binding kept), `rebind`, `host_pinned`, `delegated`. The
reason is carried separately in `cause` (for example `exhausted:weekly_exhausted`,
`auth_failed_401`, `removed`, `disabled`, `model_unavailable:opus`). Shadow mode
records the same outcome with `applied: false`.

**Rebind policy** (`rebind_policy`): `conservative` (default; rebind only on
confirmed exhaustion, auth failure, removal, disablement, model ineligibility),
`eager` (also rebind after a temporary failover succeeds), `never` (fail over
per request only; never rebind unless the account is removed).

**Failure classification** from usage records: 401/403 → auth_failed (sticky
until credential stamp changes and a poll succeeds, or a later success on that
account); 429 → quota exhaustion when provider headers say a window is
rejected or the snapshot is at 100%, otherwise rate-limited cooldown; 408/5xx/0
→ transient (circuit after 3 within 5 minutes, open 1 min doubling to 10 min);
404 on a model → model-scoped ineligibility for 10 minutes. (400 is not
classified: it usually means a malformed client request, and blocking the model
on that account would punish the account for the client's error.) A 429 when
the snapshot already shows the window full is covered by quota assessment,
which marks the account exhausted until the window's reset.

**Policies.** As in the architecture document. New sessions skip accounts below `new_session_min_remaining` while another account is rankable; when none is, those accounts are ranked by the policy rather than delegating to the host. Host priority always first;
`manual_priority` adds plugin `account_priority` next; then the base policy.

**Config** (flat keys under `plugins.configs.kpatdev-quota-router`; existing keys
kept):

| Key | Default | Range |
|---|---|---|
| `mode` | `shadow` | `shadow`, `active` |
| `strategy` | `reset_first` | `reset_first`, `quota_pressure`, `balanced`, `manual_priority` |
| `manual_priority_base` | `reset_first` | `reset_first`, `quota_pressure`, `balanced` |
| `poll_interval` | `5m` | 1m–1h |
| `max_age` | `10m` | poll_interval–1h |
| `request_timeout` | `10s` | 1s–30s |
| `passive_skip` | `1m` | 0s–1h; 0 disables regular-poll skipping |
| `passive_skip_max` | `30m` | 1m–24h; maximum successful-poll age for skipping |
| `passive_quota` | `true` | bool |
| `affinity_enabled` | `true` | bool |
| `affinity_ttl` | `3h` | 5m–7d |
| `affinity_scope` | `model` | `model`, `provider` |
| `affinity_inherit_parent` | `true` | bool |
| `affinity_max_sessions` | `20000` | 100–200000 |
| `rebind_policy` | `conservative` | `conservative`, `eager`, `never` |
| `pressure_min_hours` | `0.5` | 0.1–24 |
| `balanced_five_hour_soft_limit` | `85` | 50–100 |
| `new_session_min_remaining` | `5` | 0–50 (% left; new sessions only) |
| `schedule_across_priorities` | `true` | bool |
| `account_priority` | `{}` | map auth ID → int −1000..1000 |
| `account_alias` | `{}` | map auth ID → string ≤ 40 printable chars |
| `state_dir` | `plugins/kpatdev-quota-router-data` | path |
| `activity_retention` | `1000` | 100–20000 decisions |
| `history_retention` | `168h` | 1h–720h |

**Management API** (all under `/v0/management/plugins/kpatdev-quota-router/`,
CPA-authenticated): `GET status` (legacy shape preserved as a subset),
`GET overview`, `GET sessions`, `GET activity`, `GET decision?id=`,
`GET usage?range=`, `GET settings`, `POST settings/validate`, `POST refresh`,
`POST simulate`, `POST sessions/clear`, `GET export`. Responses are
`Cache-Control: no-store`. Mutations require `Content-Type: application/json`
and reject bodies over 64 KiB.

**Dashboard.** React + TypeScript + Vite, no runtime CDN, no analytics. Embedded
via `go:embed`, served as exact resource routes with CSP
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'`.
Settings save = validate via plugin → `PATCH` CPA's plugin config → poll until
the plugin reports the new config generation.

## Testing Decisions

- Tests exercise external behavior: router decisions for given inputs, RPC
  responses for given RPC requests, JSON from management routes, files on disk
  from the state store. No assertions on private fields.
- Deterministic time everywhere via an injected clock; no sleeps for ordering.
- Router: acceptance scenario (A/B, ranking change, exhaustion failover,
  restart, subagent) for Claude and Codex; plus the routing, affinity, and
  reliability lists from the brief.
- Session identity: table tests plus parity against CPA v8's
  `ExtractSessionInfo` (test-only import).
- Quota: parser tables (valid, missing, invalid, unit scaling), fuzzing of
  parsers, availability transitions across reset boundaries.
- State store: round trip, corrupt file quarantine, interrupted write (temp
  file left behind), lock contention.
- Management: authorization is CPA's, so tests assert no secrets in any
  response, validation errors, rate limits, and confirmation requirements.
- Frontend: Vitest for formatting and normalization helpers; Playwright
  screenshots at desktop/tablet/mobile in dark and light mode against the real
  plugin in CPA with synthetic data; axe-core accessibility pass.
- Host: existing `tests/host_smoke.py` extended to CPA v8.0.27 with session
  headers, affinity, exhaustion failover, restart persistence, Codex, streaming,
  and resource/management routes; a v7.3.15 load smoke for compatibility.
- Prior art: `engine_test.go`, `quota_test.go`, `runtime_test.go`,
  `tests/host_smoke.py`, `tests/native_smoke.py`.

## Out of Scope

- Modifying CPA core.
- Request-body session extraction (would require request interceptors that
  serialize every body across the ABI).
- Consuming quota to probe or force resets; credential writes; token refresh.
- Multi-instance shared state or external databases.
- CPA Home dispatch scheduling (host bypasses plugin schedulers).
- Mixed-provider routes.

## Further Notes

- Plugin ID is `kpatdev-quota-router` to avoid the upstream registry ID collision.
  Existing config keys, library names, and default state directories must be migrated
  as described in README.md (user-approved 2026-10-13).
- Quota endpoints are undocumented; parsers are tolerant of unknown fields and
  fail closed (unknown) on missing required windows.

Regular polls align to wall-clock multiples of `poll_interval`, with a stable
per-account offset under five seconds. Reset probes run 30 seconds before and
after each known reset. A window observed at or after probe time minus 10 seconds,
or an account polled within 10 seconds, suppresses that probe. Pre-reset probes
expire at reset; post-reset probes expire at reset plus 10 minutes. Error backoff
also gates probes. Passive observations can skip regular polls only while the
last successful poll is younger than `passive_skip_max`; manual refreshes and
reset probes bypass passive skip.
