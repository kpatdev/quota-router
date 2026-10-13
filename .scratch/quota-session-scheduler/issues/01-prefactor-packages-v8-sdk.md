# 01: Prefactor into internal packages on the CPA v8 SDK

**What to build:** The current plugin, unchanged in behavior, reorganized so later tickets can land as deep modules: C ABI shim and runtime in `main`, quota parsing/fetching, config, and selection in internal packages, built against `github.com/router-for-me/CLIProxyAPI/v8`. Existing tests move with their code and stay green.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] go.mod requires CPA v8 SDK; module path is the fork's
- [ ] `make test` (gofmt, vet, race tests) passes with the existing test cases
- [ ] Plugin ID, config keys, and status route unchanged
