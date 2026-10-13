# 08: Dashboard shell, overview, and accounts

**What to build:** React + TS + Vite dashboard embedded in the plugin and served as CPA plugin resources: management-key gate (memory only), sidebar navigation, dark/light themes, overview summary, next-to-use panel, Claude and Codex account tables with quota bars, countdowns, filters, search, sort, account detail, aliases, manual refresh.

**Blocked by:** 06, 07

**Status:** done

Done. axe WCAG 2.1 AA audit passes on all pages, both themes, desktop and mobile (`pnpm a11y`); tablet layout checked at 900px.

- [x] Resource routes serve index and hashed assets with CSP and nosniff; no data in resources
- [x] Bars render remaining capacity; tests cover utilization vs remaining inputs
- [x] Keyboard navigation and visible focus; contrast passes axe
- [x] Desktop, tablet, mobile layouts
