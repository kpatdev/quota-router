# 2. Session identity from headers at pick time

Date: 2026-10-13. Status: accepted.

CPA writes `canonical_session_id` into request metadata only after selection or
inside its own affinity selector, which a plugin scheduler bypasses, and does not
send the request body to schedulers. Claude Code and Codex both send session
headers, so the plugin derives the session from headers with CPA's precedence and
pins parity with a test against CPA's `ExtractSessionInfo`.

Rejected: a before-auth request interceptor that copies a body-derived session
into a private header. It would serialize every request body (often megabytes for
agent threads) across the ABI twice per request and leak a header upstream unless
also stripped after auth.
