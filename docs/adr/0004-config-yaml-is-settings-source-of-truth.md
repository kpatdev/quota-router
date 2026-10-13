# 4. CPA config.yaml is the settings source of truth

Date: 2026-10-13. Status: accepted.

Dashboard settings are saved through CPA's `PATCH /v0/management/plugins/<id>/config`,
which persists `config.yaml` and hot-reloads the plugin. A plugin-side override
file would create two sources that disagree after a manual YAML edit. Because an
invalid value in `config.yaml` would fail registration at the next restart, the
dashboard validates through the plugin's own `settings/validate` route first.
