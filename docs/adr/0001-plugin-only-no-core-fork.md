# 1. Plugin only; no CLIProxyAPI core changes

Date: 2026-10-13. Status: accepted.

Every required feature is reachable through the public plugin SDK: `scheduler.pick`
for selection, `usage.handle` for per-attempt outcomes and response headers,
`host.auth.list`/`host.auth.get` for the roster and polling credentials, and
management routes plus resources for the dashboard. The gaps (no request body at
pick time, no pick↔usage correlation ID, no Home-dispatch scheduling) are
documented limitations rather than reasons to fork. A fork would have to be
rebased onto a host that ships several releases a week.
