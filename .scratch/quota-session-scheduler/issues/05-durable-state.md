# 05: Durable state across reload and restart

**What to build:** A versioned JSON state store (bindings, account health, snapshots, quota history) with atomic writes, ≤1/s debounce, flush on quiesce/shutdown, corrupt-file quarantine, and an advisory lock; runtime keeps router state across `plugin.reconfigure`.

**Blocked by:** 04

**Status:** done

Done. TestCorruptFileIsQuarantined, TestSecondProcessIsReadOnly, no-secrets assertion in TestNoSecretsInAnyManagementResponseOrStateFile.

- [x] Restart test: binding and rebinding survive a new runtime instance over the same directory
- [x] Corrupt or truncated file → quarantined, plugin starts empty, status reports it
- [x] Second process on same dir runs without persistence and reports it
- [x] No secrets or raw session IDs in the file (asserted)
