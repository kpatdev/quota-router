# 04: Poll through the account's proxy

**What to build:** Polls use the credential's `proxy_url` when set; otherwise the same proxy precedence CPA uses (global proxy-url, environment, direct). If an account `proxy_url` cannot be parsed, fail that poll with a typed error; never fall back to direct. Stop rejecting accounts that have `proxy_url` (keep the `base_url` skip). Secrets in proxy URLs must never be logged or shown.

**Blocked by:** 03 (touches the same poll path)

**Status:** done

Implemented cached account proxy transports, sanitized failures, and environment/direct fallback; documented the native API global-proxy limitation and redirect-safety constraint.

- [x] Tests for account proxy precedence, environment/direct fallback, supported schemes, transport reuse, parse failure, sanitized errors/poll_error, and retained base_url skip
- [x] Document global CPA proxy limitation in limitations and configuration reference
- [x] go test ./..., go vet ./..., and go test -race ./internal/... pass
