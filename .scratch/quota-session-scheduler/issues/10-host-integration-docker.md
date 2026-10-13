# 10: Host integration on CPA v8.0.27 and v7.3.15, Docker persistence

**What to build:** Host smoke tests that run the official CPA releases with the plugin, synthetic credentials, and a local TLS fixture for both providers: session affinity, ranking change, exhaustion failover, restart persistence, subagent inheritance, streaming and non-streaming, management and resource routes. A Docker Compose example with a state volume, verified across a container restart.

**Blocked by:** 06, 08

**Status:** ready-for-agent

- [ ] linux_amd64 and linux_arm64 plugin builds load in CPA v8.0.27
- [ ] v7.3.15 loads the same binary and routes
- [ ] Binding survives `docker compose restart`
