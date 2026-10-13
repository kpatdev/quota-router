# Architecture

quota-router is a native CLIProxyAPI (CPA) plugin. It schedules Claude and Codex
subscription accounts by quota, keeps agent threads on one upstream account, and
serves an operator dashboard. It runs entirely through the public plugin SDK; CPA
core is not modified.

Facts below were verified against CPA v8.0.27 (2026-10-12) and v7.3.15 source.
File references are to the CPA repository.

## Host contract

| Item | v7.3.15 | v8.0.27 | Consequence |
|---|---|---|---|
| Native C ABI (`pluginabi.ABIVersion`) | 1 | 1 | One binary per OS/arch serves both. |
| RPC schema (`pluginabi.SchemaVersion`) | 6 | 6 | Same JSON contract. v8 only adds `host.http.operation_open`, `host.http.cancel`, `host.routing.reset_cooldown`, and `Reject*` on scheduler responses. The plugin uses none of the v8-only additions. |
| Module path | `.../CLIProxyAPI/v7` | `.../CLIProxyAPI/v8` | The plugin builds against the v8 SDK types; the wire format is identical. |

Capabilities used (declared in `plugin.register`):

- `scheduler` + `scheduler_across_priorities` — `scheduler.pick`.
- `usage_plugin` — `usage.handle`, one record per upstream attempt.
- `management_api` — authenticated JSON routes plus unauthenticated static resources.

Host callbacks used: `host.auth.list`, `host.auth.get`.

Not used: request interceptors (they would serialize every request body across
the ABI), `quota.*` provider (management-only display), `host.affinity.lookup`
(returns `unsupported` whenever any plugin scheduler is active —
`sdk/cliproxy/auth/conductor_selection.go` `LookupSessionAffinity`).

## What the scheduler actually receives

`scheduler.pick` is called from `pickViaPluginScheduler`
(`sdk/cliproxy/auth/conductor_selection.go`). The request carries:

- `Provider`/`Providers`, `Model` (route model, thinking suffix stripped for matching), `Stream`.
- `Candidates`: ID, provider, host priority, status, and **safe** attributes
  (keys containing token/secret/cookie/credential/... are stripped). Candidate
  `Metadata` is **not** sent. Candidates are already filtered by the host for
  provider, model support, disabled state, cooldown/availability, credential
  policy, pinned auth, and auths already tried in this request.
- `Options.Headers`: the full inbound client headers, **including the client's
  proxy API key** (`Authorization`). The plugin reads session headers only and
  never stores, logs, or echoes headers.
- `Options.Metadata`: `requested_model`, `request_path`, `idempotency_key`,
  `pinned_auth_id`, `execution_session_id` (WebSocket), `caller_scope`,
  `session_affinity_provider`/`_model`, and others.

`canonical_session_id` and `parent_session_id` are **not** reliably present at
pick time: the host writes them later (after selection, in
`ensureCanonicalSessionMetadata`) or only inside its own
`SessionAffinitySelector`, which a plugin scheduler bypasses. The request body
is not sent to schedulers.

Therefore session identity at pick time comes from **headers**, using the same
precedence as CPA's `session.ExtractSessionInfo(headers, nil, metadata)`
(`sdk/cliproxy/session/info.go`):

| Client | Session | Parent / subagent |
|---|---|---|
| Claude Code | `X-Claude-Code-Session-Id` | `X-Claude-Code-Agent-Id`, `X-Claude-Code-Parent-Agent-Id` → `claude:<sid>:agent:<id>` with parent `claude:<sid>` |
| Codex CLI | `Session-Id`/`Session_id`, `Thread-Id`, `X-Codex-Turn-Metadata` (`session_id`, `thread_id`) | `X-Codex-Parent-Thread-Id`, turn-metadata `parent_thread_id`, `forked_from_thread_id`, `subagent_kind`, `X-Openai-Subagent` |
| Generic | `X-Session-ID`, `X-Session-Affinity`, `X-Slot-Session-Id`, `X-Task-ID`, `X-Conversation-Id`, `X-Thread-Id` | `X-Parent-*` variants |
| WebSocket | `execution_session_id` metadata | — |

`X-Client-Request-Id` is deliberately excluded: it is a per-request ID for
several clients. Request IDs are never treated as conversation IDs. Session IDs
derived only from the request body (Claude `metadata.user_id`, Codex
`prompt_cache_key`) cannot be used at pick time; those requests get quota-aware
selection without persistent affinity.

A unit test compares the plugin's extractor against CPA's own
`ExtractSessionInfo` on a fixture corpus so drift is caught on SDK upgrades.

## Feedback

`usage.handle` delivers a `UsageRecord` for every upstream attempt, successful
or failed (`internal/runtime/executor/helps/usage_helpers.go`). Used fields:

- `AuthID`, `Provider`, `Model`, `SessionID`, `ParentSessionID` (same
  `claude:`/`codex:` strings as header extraction when headers were present).
- `Failed`, `Failure.StatusCode` → failure classification.
- `Detail` token counts including cache read/creation → usage and cache metrics.
- `ResponseHeaders` → passive quota observation (`Anthropic-Ratelimit-Unified-*`,
  `X-Codex-Primary-*`/`Secondary-*`).

Ignored and never stored: `APIKey`, `Failure.Body`, other headers.

There is no correlation ID between a pick and its usage record. Results are
attributed by (provider, session key, auth ID). Requests without a session are
counted per account but not joined to a specific decision.

## Selection pipeline

```
pick(req)
  provider ∉ {claude, codex} or mixed → not handled (host decides)
  key := session(headers, metadata, caller_scope)          # may be empty
  pinned_auth_id present → choose that candidate, never rebind against it
  binding(key) exists:
      bound account offered and usable      → keep (reason: affinity)
      bound account permanently unusable    → choose new, rebind (reason: rebind:<cause>)
      bound account temporarily unusable    → choose new for this request, keep binding (failover)
  no binding:
      parent binding usable and inheritance on → inherit
      else rank usable candidates by policy    → bind
  nothing usable with trustworthy quota → not handled (host built-in scheduler)
mode = shadow → record decision, return not handled
```

"Usable" requires: offered by the host; not disabled; not auth-failed; not
confirmed exhausted for the requested model's windows; circuit not open.
Unknown or stale quota does **not** break an existing binding (staleness is not
evidence of failure) but does exclude the account from new-session ranking.

Rankings respect host priority first (higher tier wins while any account in it
is usable), then optional plugin priority (`manual_priority`), then the policy:

- `reset_first` (default): earliest long-window reset (weekly, or monthly when
  that is the account's long window).
- `quota_pressure`: remaining long-window % ÷ max(hours to reset, 0.5)
  (Jeffery Zhang's formula).
- `balanced`: reset-first, but accounts above the five-hour soft limit are
  ranked after accounts below it.

Ties break by remaining percentage, then auth ID.

New sessions skip accounts whose most constrained window has less than
`new_session_min_remaining` percent left (default 5, reason `low_headroom`),
unless nothing else is usable. Reset-first would otherwise start long
conversations on an account that is nearly empty because it resets soonest.
Bound sessions are not affected: they stay until the account is actually
exhausted.

## Quota model

Each account has a snapshot of windows with explicit semantics:

| Window | Claude source | Codex source |
|---|---|---|
| five-hour | `five_hour` | window with `limit_window_seconds = 18000` |
| weekly | `seven_day` | `604800` |
| monthly | — | `2419200–2678400` |
| model weekly | `seven_day_<family>` (sonnet, opus, fable, …) | — |

All values are stored as **used percent 0–100**. Claude passive headers report
utilization as a 0–1 fraction and are scaled; Codex headers already report
percent. Remaining = 100 − used is computed in one place.

Evidence rules:

- A window past its reset time is `reset_pending` until fresh data arrives; the
  plugin schedules a poll shortly after each reset. It never assumes a reset.
- Snapshots older than `max_age` are `stale`.
- Passive observations update windows only when the header set is complete.
- A 429 that the provider headers attribute to a window marks it exhausted
  until that window's reset (verified by the next poll).

Polling uses read-only `GET` usage endpoints, never inference, with per-account
exponential backoff and jitter, a poll shortly after each reset, and a
rate-limited manual refresh.

## Persistence

State lives in `state_dir` (default `plugins/kpatdev-quota-router-data`, which the
stock compose file already mounts as a volume):

- `state.json`: bindings, account health, quota snapshots, quota history, settings
  generation. Written atomically (temp file, fsync, rename, fsync dir), debounced
  to at most once per second and on shutdown. Corrupt files are moved aside and
  the plugin starts empty.
- `state.lock`: advisory `flock`. A second process sharing the directory runs
  without persistence and reports it on the dashboard.

Session keys are stored as SHA-256 hashes of (caller scope, session ID). No
credentials, tokens, prompts, request bodies, or API keys are persisted.

JSON was chosen over SQLite: the state is small and bounded, writes are
whole-document, and a pure-Go SQLite would add several MB and cgo-free build
complexity for no integrity gain over atomic rename (ADR 0003).

## Dashboard

- Static React + TypeScript bundle embedded in the plugin binary and served as
  plugin resources under `/v0/resource/plugins/kpatdev-quota-router/`. Resources
  are unauthenticated, so they contain no data.
- All data comes from management routes under
  `/v0/management/plugins/kpatdev-quota-router/`, which CPA authenticates with the
  management key. The key is held in memory only.
- Settings are written through CPA's own `PATCH
  /v0/management/plugins/kpatdev-quota-router/config`, so `config.yaml` stays the
  single source of truth and CPA hot-reloads the plugin. The plugin validates
  first via its own endpoint.

## Compatibility boundaries and gaps

- **CPA Home dispatch** (`home.enabled: true`): local plugin schedulers and
  plugin resources are bypassed by the host. The plugin stays loaded but receives no
  picks, so the Activity page shows no new decisions. The plugin cannot detect
  Home mode itself because it never sees CPA's configuration.
- **WebSocket**: CPA pins a Responses WebSocket connection to one account
  (`pinned_auth_id`); the plugin only ever sees that candidate and records it.
  Affinity across connections works through the session headers.
- **Mixed-provider routes** are left to the host.
- **Body-only session IDs** get no persistent affinity (see above).
- **Multiple proxy instances** sharing one state directory: only the lock holder
  persists. Bindings are per instance; a load balancer in front of several
  proxies must itself be sticky.
- **Credential proxies**: accounts with `proxy_url`/`base_url` are not polled
  (polling would bypass the proxy); passive observation still applies.
