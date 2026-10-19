# Limitations

Known limits of kpatdev-quota-router 1.0.0, grouped by cause. Each item says what happens and what to do about it.

## Imposed by CPA's plugin interface

- **No request body at pick time.** Session identity comes from headers only. Clients whose session ID appears only in the body (Claude `metadata.user_id`, Codex `prompt_cache_key`) get quota-aware routing per request but no persistent affinity. Claude Code and Codex CLI send session headers, so they are unaffected.
- **No canonical session ID at pick time.** CPA computes `canonical_session_id` after selection. The plugin re-derives the session from headers with the same precedence. A unit test pins this to CPA's extractor, so drift will surface on SDK upgrades, but a future CPA change to header handling needs a plugin update.
- **CPA Home dispatch** (`home.enabled: true`) bypasses plugin schedulers entirely. The plugin stays loaded and its dashboard works, but it routes nothing. The plugin cannot detect Home mode.
- **Mixed-provider requests** (one request whose candidates span Claude and Codex) are left to CPA's own routing.
- **WebSocket connections** are pinned by CPA to one account for their lifetime (`pinned_auth_id`). The plugin records the choice but cannot move the connection. Affinity across connections still works through session headers.
- **Candidate filtering happens first.** CPA removes cooled-down, disabled, model-ineligible, and already-tried accounts before the plugin sees the list. Two consequences:
  - After a 5xx, CPA applies its own cooldown (about 60 s, shorter with `Retry-After`) on top of the plugin's circuit breaker.
  - A bound account in CPA cooldown simply isn't offered. The plugin fails over for that request and keeps the binding.
- **Usage records arrive asynchronously**, one per upstream attempt, after the response. Health changes (429, 401, 5xx) take effect from the next pick, not the attempt in flight. CPA's own retry covers the attempt in flight.
- **Native plugins run in CPA's process** with access to its credentials and traffic. Expected errors and panics in `pick` fall back to CPA routing. A crash in native code can still take CPA down.

## Quota data

- **The quota endpoints are undocumented** (`/api/oauth/usage`, `/backend-api/wham/usage`) and can change without notice:
  - Parsers ignore unknown fields.
  - If a required window is missing, the account is marked unknown, which means it takes no new sessions.
  - If every account is unknown, routing falls back to CPA.
- **Polling cadence bounds freshness.** Between polls (default 5 min) the plugin relies on rate-limit headers from normal traffic (`passive_quota`). An idle account changes only at the next poll. Quota older than `max_age` (default 10 min) is stale:
  - Stale data does not move existing bindings.
  - Stale accounts take no new sessions.
- **Resets are not assumed.** A window whose reset time has passed stays `reset_pending` until a poll confirms fresh quota. The plugin polls shortly after each reset, so the gap is usually under a minute. During it, a bound session on that account keeps its binding, but the account is not used for new sessions.
- **Burn-rate estimates** (time to exhaustion) are a least-squares fit over recent quota samples within the current window. They need several samples, so they appear about 15 minutes after start or a reset. They assume the recent pace continues.
- **Codex windows** are classified by `limit_window_seconds` (5 h, 7 d, 28–31 d); passive headers use the equivalent `Window-Minutes` values. Unfamiliar lengths are skipped with a debug log while recognized windows are retained. Missing lengths default to five-hour for the primary window and weekly for the secondary window. If skipping an unfamiliar long window leaves no recognized long window, the account assesses as unknown for new sessions.
- **Credentials with `base_url`** are not polled. Passive headers still update them. Account `proxy_url` is supported; malformed settings fail closed with `invalid_proxy_url`. Poll transports are cached per proxy URL and released on shutdown.
- **Global CPA `proxy-url` is unavailable separately to native scheduler plugins.** CPA v8.0.27 exposes it through provider contexts, but not this plugin's lifecycle/auth callbacks. `host.http.do` applies the global proxy but follows redirects without a disable option. To preserve bearer-token redirect protection, quota polls instead use account `proxy_url`, then `http.ProxyFromEnvironment`, then direct. Configure an account proxy or process environment proxy when polling must use a proxy.
- **Token counts are not costs.** The Usage page reports what CPA reports per attempt (requests, input, output, cache reads and writes). It does not estimate money.

## Routing behaviour

- **Affinity is per model by default** (`affinity_scope: model`). A session that uses Opus for the main thread and Haiku for subagents can hold one binding per model, possibly on different accounts. Set `affinity_scope: provider` to keep a session on one account for all models of a provider.
- **The new-session minimum** (`new_session_min_remaining`, default 5 %) keeps new sessions off nearly empty accounts while another account is ready. When every ready account is below it, the plugin still chooses among them by policy. Bound sessions keep using an account until it is actually exhausted.
- **Reset-first concentrates load.** By design, every new session goes to the soonest-resetting account until it runs low. Use `balanced` to spread five-hour usage, or `quota_pressure` to pace by remaining quota per hour.
- **Bindings expire after `affinity_ttl` of inactivity** (default 3 h). A session resumed after that is routed as new, possibly to a different account. Its prompt cache would have expired anyway for most providers.

## Deployment

- **One CPA instance per state directory.** The state file is protected by an advisory lock. A second instance pointed at the same directory runs without persistence and the dashboard shows a warning. Bindings are not shared across instances, so a load balancer in front of several proxies must itself be sticky by session.
- **Persistence is debounced** (at most once per second, plus on shutdown). A hard kill (`SIGKILL`, power loss) can lose up to about a second of new bindings. The state file is never left half-written: writes go to a temp file and are atomically renamed.
- **Docker:** state is persisted only if `state_dir` is inside a mounted volume. The default (`plugins/kpatdev-quota-router-data`) is inside the `plugins` mount of the provided compose file.
- **Clock:** reset countdowns and TTLs use the host clock. A badly skewed clock shows wrong countdowns. Ranking is affected only when the skew is comparable to the time left until reset.

## Dashboard

- **The management key is entered per tab** and held in memory only. Reloading the page asks for it again. This is deliberate: the key is never written to browser storage.
- **Account IDs appear in management API responses** because actions such as refresh need them. The UI displays them shortened, or uses your `account_alias`; exports replace them with opaque handles. Anyone with the management key can see full IDs.
- **Data is polled**, not pushed: every 5 s for activity, 10 s for the overview, accounts, and sessions, 30 s for usage. The UI can be that far behind.
- **Tested in Chromium only** (desktop and mobile emulation). Other evergreen browsers are expected to work but were not tested.

## Testing

- All automated tests use synthetic accounts and local TLS fixtures. The provider response and header formats come from the original plugin's parsers and CPA's own handling of these providers. They have not been verified against live endpoints in this work.
- Codex on CPA v7.3.15 is untested (the HTTP/1.1 fixture cannot serve v7's HTTP/2 Codex client). See [compatibility.md](compatibility.md).
- Real Claude Code and Codex CLI binaries were not run against the proxy. Their session headers are taken from CPA's extractor.
