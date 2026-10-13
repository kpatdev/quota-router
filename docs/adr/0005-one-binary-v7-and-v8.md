# 5. One binary for CPA v7.3.15+ and v8

Date: 2026-10-13. Status: accepted.

Both host lines use native ABI 1 and RPC schema 6; v8 changes are additive. The
plugin builds against the v8 SDK types and avoids v8-only callbacks, so the same
library loads in both. Host tests run against v8.0.27 (primary) and v7.3.15
(compatibility).
