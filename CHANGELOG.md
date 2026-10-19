# Changelog

## 1.0.0 (unreleased)

A rewrite of the original quota-reset-router 0.2.1 scheduler into a session-aware, quota-aware scheduler with a dashboard. Existing configuration keeps working after the [plugin ID migration](README.md#migrating-from-quota-reset-router).

### Breaking

- **Plugin ID is now `kpatdev-quota-router`** (was `quota-reset-router`). This renames the config key, library file, release zip, management and resource paths, and default state directory. It avoids colliding with upstream in the CPA plugin store.
- **Built against the CPA v8.0.27 SDK.** ABI 1 and schema 6 are unchanged, and the same library loads in CPA v7.3.15.
- **New sessions skip nearly empty accounts** while another is ready (`new_session_min_remaining`, default 5 %). Set it to `0` for the original behaviour.

### Added

- **Quota observation improvements.**
  - Claude's Fable weekly limit is read from the usage `limits[]` list and the `7d_oi` headers, so Fable requests avoid accounts that have used it up.
  - Regular polls align to the clock. Extra polls run 30 s before and after each known reset.
  - A scheduled poll is skipped when response headers updated the account within `passive_skip` (default 1 m), until the last poll is older than `passive_skip_max` (default 30 m).
  - Polls use the account `proxy_url`, then environment proxies, then direct. A malformed proxy fails the poll instead of connecting directly.
  - A CPA cooldown that outlasts fresh, available quota is flagged on the dashboard and in skip reasons. It is never cleared automatically.
  - A Codex window of unfamiliar length is skipped instead of failing the poll.

- **Session affinity.**
  - Sessions are identified from Claude Code, Codex CLI, and generic session headers, using CPA's own precedence. A test checks parity with CPA's extractor.
  - The session ID is hashed with the client's key scope, so clients never share a session.
  - A session stays on its account while that account is usable.
  - Subagents inherit their parent session's account.
  - Bindings expire after `affinity_ttl` and are capped at `affinity_max_sessions` with LRU eviction.
  - Binding scope is per model or per provider.
- **Failure handling from usage records.**
  - 401/403: account marked auth-failed until fresh evidence.
  - 429: window exhausted when the provider headers say so, otherwise a rate-limit cooldown.
  - 5xx and timeouts: circuit breaker (3 within 5 min; 1 min doubling to 10 min).
  - 404 on a model: model-scoped block for 10 minutes. (400 is not classified, because it usually means a malformed client request.)
  - Temporary failures fail over for one request and keep the binding. Lasting failures rebind, per `rebind_policy` (`conservative`, `eager`, `never`).
- **Policies:** `quota_pressure` (remaining ÷ hours to reset), `balanced` (five-hour soft limit), and `manual_priority` (`account_priority`, then a base policy), alongside the original `reset_first`.
- **Quota model.**
  - Five-hour, weekly, monthly, and per-model weekly windows, with explicit states (`ready`, `exhausted`, `reset_pending`, `stale`, `unknown`).
  - Passive updates from rate-limit headers on normal traffic (`passive_quota`).
  - A poll shortly after each reset, per-account backoff, and rate-limited manual refresh.
  - A passed reset time is never treated as a reset until fresh data confirms it.
- **Persistence:** bindings, account health, and quota cache survive restarts, plugin reloads, and Docker container recreation. Atomic JSON writes, corruption quarantine, and an advisory lock against two instances sharing a directory.
- **Dashboard** at `/v0/resource/plugins/kpatdev-quota-router/index.html`, embedded in the library.
  - Overview with the next account for new sessions.
  - Accounts & Quotas with burn-rate estimates.
  - Sessions with subagent lineage.
  - Activity with a decision explorer.
  - Usage charts (requests, tokens, cache, affinity, switches).
  - Settings with validation, a policy simulator, and redacted export.
  - Dark and light themes, desktop and mobile.
- **Management API:** `overview`, `accounts`, `sessions`, `activity`, `decision`, `usage`, `settings`, `settings/validate`, `refresh`, `simulate`, `sessions/clear`, `export`.
- **Configuration keys:** `strategy`, `manual_priority_base`, `account_priority`, `account_alias`, `new_session_min_remaining`, `pressure_min_hours`, `balanced_five_hour_soft_limit`, `schedule_across_priorities`, `affinity_*`, `rebind_policy`, `passive_quota`, `state_dir`, `activity_retention`, `history_retention`.
- **Capabilities** `usage_plugin` and `scheduler_across_priorities`.
- **Docker:** `deploy/docker-compose.yml` and `deploy/config.example.yaml` for the official CPA image, and a persistence test against it.
- **Tests:**
  - Router and plugin acceptance tests for both providers.
  - The CPA host scenario on v8.0.27 (Claude and Codex) and v7.3.15 (Claude).
  - The Docker persistence test.
  - Dashboard unit tests.
  - CI checks that the committed dashboard bundle matches its sources.
- Documentation: spec, architecture, ADRs, glossary, [compatibility matrix](docs/compatibility.md), [limitations](docs/limitations.md).

### Changed

- **Ranking:** CPA priority, then plugin priority (`manual_priority` only), then the policy score, then remaining quota, then ID. The original ranked by weekly reset, then ID.
- **Settings reload keeps state.** The original rebuilt its engine on every reconfigure, discarding cached quota; bindings, quota, and history now persist across reloads.
- **The status route** keeps every original field and value (`selection_policy` is still `weekly_reset_first` under the default policy). It adds `strategy`, `sessions`, and `counters`.

### Security

- No OAuth tokens, refresh tokens, authorization headers, API keys, request bodies, or prompt text are stored, logged, exported, or returned. Every management route and the state file are checked for this in tests.
- Session IDs are persisted only as truncated SHA-256 hashes.
- The dashboard holds the management key in memory only and serves static assets under a strict CSP with no third-party origins.

## 0.2.1 and earlier

See the [upstream repository](https://github.com/WebDevCaptain/quota-reset-router) history: a weekly-reset-first scheduler for Claude and Codex with background quota polling, shadow and active modes, and a status route.
