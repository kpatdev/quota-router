# 01: Prefactor into internal packages on the CPA v8 SDK

**What to build:** The current plugin, unchanged in behavior, reorganized so later tickets can land as deep modules: C ABI shim and runtime in `main`, quota parsing/fetching, config, and selection in internal packages, built against `github.com/router-for-me/CLIProxyAPI/v8`. Existing tests move with their code and stay green.

**Blocked by:** None (can start immediately)

**Status:** done

Done. Plugin ID later renamed to `kpatdev-quota-router` (user-approved 2026-10-13, see AGENT-COORDINATION.md); config keys and status-route fields are otherwise unchanged (TestStatusKeepsTheOriginalShape).

- [x] go.mod requires CPA v8 SDK; module path is the fork's
- [x] `make test` (gofmt, vet, race tests) passes with the existing test cases
- [x] Plugin ID, config keys, and status route unchanged
