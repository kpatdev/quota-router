# 05: Durable state across reload and restart

**What to build:** A versioned JSON state store (bindings, account health, snapshots, quota history) with atomic writes, ≤1/s debounce, flush on quiesce/shutdown, corrupt-file quarantine, and an advisory lock; runtime keeps router state across `plugin.reconfigure`.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] Restart test: binding and rebinding survive a new runtime instance over the same directory
- [ ] Corrupt or truncated file → quarantined, plugin starts empty, status reports it
- [ ] Second process on same dir runs without persistence and reports it
- [ ] No secrets or raw session IDs in the file (asserted)
