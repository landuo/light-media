# Ordinary manager registry/configuration queries

Pinned source: MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`.
This slice exposes only `get_plugin_ids`, `get_running_plugin_ids`, and
`get_plugin_config` through the existing authenticated resource SDK scope.

IDs preserve actual registry insertion order, including initialized-disabled
objects and imported classes whose initialization failed. Configuration is the
manager getter, not Base: absent class/falsy value yields `{}`, empty keys are
removed, and queries never initialize an object or save configuration. Ordinary
cross-plugin namespaces use separately declared capabilities; there is no
arbitrary settings, credential, method execution, or lifecycle endpoint.

`query-source.json` is a compact projection of a newly executed source component
witness using unchanged whole manager/Base/DB/operator modules, real SQLite,
owned loader packages and a second Python process. Explicit unused integration
imports/event sinks are fixtures; this is not source daemon/authentication E2E.

Build the owned worker variants Alpha/Beta/Broken/Denied and `cmd/server` with
`-race` (see the variant defaults in the fixture), then set
`OWNED_MANAGER_{ALPHA,BETA,BROKEN,DENIED}_BINARY` and
`OWNED_MANAGER_SERVER_BINARY` to their paths. For the independent mid-start
outage case, also build an OwnedBeta variant with `-X main.saveObservations=false`
and set `OWNED_MANAGER_BETA_READONLY_BINARY`. This skips only fixture trace writes,
not SDK queries. Run:

```sh
go test -race ./internal/app -run '^TestSharedManagerQueries' -count=1
```

The fixture's ordinary form/page displays these safe queries using a configured
target plugin ID. Tests use isolated temporary accounts, ordinary synthetic
configurations and loopback artifact servers; no real credentials or remote
business services. State getters, descriptors, init/stop/reload callbacks and
scheduler mutation are separate, still-unverified S10 capabilities. Existing
serial/concurrent SDK behavior and resource/frame limits are unchanged.

The required configuration producer shares the same source-shaped cache: startup
prefetches ordinary plugin configuration before loading classes, and synchronous
HTTP saves publish valid values even when SQL fails (without initializing on
failure). Same-value writes, falsey deletion, warm cache and cold restoration are
tested with real workers/SQLite. A successful SQL read still checks native-write
fingerprints and encrypted integrity; invalid seals are never hidden by cache.
No global settings capability or weaker invocation admission is introduced.
