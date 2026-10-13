# Plugin ID rename coordination

2026-10-13: The user approved changing this fork's plugin ID to `kpatdev-quota-router` to avoid the upstream `quota-reset-router` ID already present in the CLIProxyAPI plugin registry.

The agent working in `/Users/ksp/code/CLIProxyAPI-Plugins-Store` is updating the source ID, build/package filenames, test routes, and current documentation, then adding the registry entry on branch `add-quota-router`.

Existing edits in `Makefile`, `internal/plugin/runtime.go`, and `tests/host_smoke.py` belong to ongoing work and will be preserved. Upstream attribution remains intact. Please use `kpatdev-quota-router` for future config keys, management/resource paths, libraries, and release ZIPs. Existing installations need their config key and default state directory migrated; no automatic migration is being added.

A published release with the new filenames and `checksums.txt` is still required for store installation. No commit, push, or release publication is part of this change.

Completed: source/config ID, Makefile, packaging script, CI ZIP references, smoke/unit test routes, README migration instructions, architecture/spec references, and third-party notice title updated. Registry entry added in the store repo. `make test` passed (formatting, vet, race-enabled tests); registry schema/field/unique-ID validation passed. Published release assets remain pending.
