# 01: Claude Fable weekly window

**What to build:** Observe the Claude Fable model-weekly limit, which we currently miss. Poll: parse `limits[]` entries with `kind == "weekly_scoped"` whose `scope.model.display_name` is `fable` or `fable <n>` (case-insensitive), reading `percent` (used) and `resets_at`; prefer an `is_active` entry when several match. Map to a model-weekly window for family `fable`. Do not double count if `seven_day_fable` is also present (prefer the top-level key). Passive: parse `Anthropic-Ratelimit-Unified-7d_oi-Utilization` (fraction 0-1) and `-Reset` into the same window.

**Blocked by:** none

**Status:** done

**Note:** Poll and passive Fable observations feed the model-weekly window used for model assessment.

- [x] Poll and passive parsing produce a fable model-weekly window
- [x] A Fable request is not routed to an account whose Fable window is full
- [x] Tests with fixture payloads and headers
