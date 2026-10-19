# 05: Flag a CPA cooldown that outlives the quota

**What to build:** If the host exposes per-account unavailability with a retry time, flag an account as `cooldown_stale` when: the cooldown has more than 10m left, the account has a five-hour window, every window has an observation younger than 15m, and every window has more than 0.01% remaining. Show it on the dashboard account row and in decision skip reasons. Do not clear the cooldown automatically. If the host API does not expose the data, record that in docs/limitations.md instead.

**Blocked by:** 01, 02

**Status:** done
**Note:** Added stale CPA cooldown detection, account API visibility, dashboard guidance, and decision skip reasons without clearing CPA cooldowns.
