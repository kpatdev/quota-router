# 09: Dashboard sessions, activity explorer, usage, simulation, settings

**What to build:** Sessions view (redacted IDs, account, model, age, activity, failover history, clear with confirmation), routing decision explorer, usage/cache charts from real metrics only, policy simulation versus actual routing, settings form with validation, change descriptions, and save through CPA's config API, plus diagnostics export.

**Blocked by:** 08

**Status:** done

Done. Settings flow in web/src/settingsApi.ts; clear-sessions confirmation tested in TestManagementMutationsAreGuarded; charts show empty states.

- [x] Settings save validates first, patches CPA config, and confirms the plugin picked up the new generation
- [x] Clearing all bindings requires typed confirmation
- [x] Charts show empty states when metrics are absent
