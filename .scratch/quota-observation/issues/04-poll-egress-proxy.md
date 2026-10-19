# 04: Poll through the account's proxy

**What to build:** Polls use the credential's `proxy_url` when set; otherwise the same proxy precedence CPA uses (global proxy-url, environment, direct). If an account `proxy_url` cannot be parsed, fail that poll with a typed error; never fall back to direct. Stop rejecting accounts that have `proxy_url` (keep the `base_url` skip). Secrets in proxy URLs must never be logged or shown.

**Blocked by:** 03 (touches the same poll path)

**Status:** open

- [ ] Tests for each precedence step and the parse-failure case
