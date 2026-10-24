# Standard dashboard metadata route correction

The standard manager replacement omitted `GET /api/plugins/dashboard/meta`, used by the real `PluginDashboard.vue` page. This omission also exists in published module commit `026591a5`; it is not introduced by restored plugin business code. A new actual HTTP test against that unchanged production source returns `404 NOT_FOUND`. This record preserves the omission rather than retroactively claiming the earlier module acceptance covered the global dashboard page.

The correction snapshots the existing loaded-object registry in insertion order, checks `get_dashboard` before `get_state`, and implements the fixed MoviePilot `get_plugin_dashboard_meta` behavior. A missing metadata method uses the plugin name and an empty key; an implemented but empty metadata method does not fall back. Exceptions are isolated to the affected plugin, and an invalid non-falsey metadata row discards that plugin's entire list comprehension. Explicit clone display names come from the authenticated clone declaration without rewriting or replacing the copied executable identity. No object initialization, queue, receipt, installed-flag gate, or legacy native fallback is introduced.

## Evidence

- Unchanged main026 plus only the new test/fixture: actual HTTP 404 regression reproduced.
- Main026 plus the two production files and tests: six top-level actual HTTP/SDK tests passed ordinary and race, with no skips.
- The same production correction in the restoration combination, plus the separate HTTP-client budget regression test: seven tests passed ordinary and race.
- The original manager source is checked at `get_plugin_dashboard_meta` and clone `_modify_python_file`; the default clone name follows the copied class's `plugin_name` semantics.
- Our cloud Chrome exercised the same production `PluginDashboard.vue` component in an isolated test transport: actual metadata candidate, cancel without saving, save and render, browser-reload persistence, hide/save/reload and show again all passed. The backend was the restoration v12 candidate containing byte-identical metadata helper logic; this was not the production authentication shell. The transport did not synthesize metadata. Earlier Plugins-page-only tests had not covered this route.
- Browser scope excludes production login, MPServerStatus public-network behavior, automatic timed refresh (refresh was set to zero), layout resizing and responsive breakpoints. The owned test service exited normally.

## Reproduce the focused tests

Build `./cmd/owned-standard-fixture` three times: default, `-ldflags '-X main.fixtureDashboardMethods=multi'`, and `-ldflags '-X main.fixtureDashboardMethods=none'`. Set `OWNED_STANDARD_FIXTURE_BINARY`, `OWNED_STANDARD_DASHBOARD_META_FIXTURE_BINARY`, and `OWNED_STANDARD_NO_DASHBOARD_FIXTURE_BINARY` to those actual executables. Run `go test -count=1 ./internal/app -run '^TestOriginalDashboardMetadata'`, then the same command with `-race`. The variants are owned test executables, not production plugin implementations.

This isolated correction does not include the CategoryEditor, SystemProcess or MPServerStatus restoration batches, bundle distribution, or production login changes. It does not claim a full main test suite or real login-browser acceptance.

The separate restoration batch is not represented as fully accepted: its first final-v12 full ordinary run recorded 846 passing roots, one existing asynchronous subscription-close test failure and two explicit external-service skips. The single failure remains preserved and is being investigated; five same-byte focused repetitions passed, which is not a substitute for the final complete gate. No subscription code is changed by this hotfix.
