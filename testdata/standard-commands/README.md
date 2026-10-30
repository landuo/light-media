# Ordinary `/clear_cache` command

Pinned MoviePilot: `8a83efeacd3e26b82be495e17d50146e31c96a60`.
Source chain: `Command.command_event/execute` → `SystemChain.remote_clear_cache`
→ `ChainBase.clear_cache/run_module`, followed by the ordinary notification chain.

`clear-cache-source.json` is a compact projection of a newly executed source
component witness, not a target-generated expected result. `null` and all-null
SDK tuples continue; lists aggregate/continue; `false`, zero, empty strings and
objects stop subsequent providers. The completion notification does not enter
message history. The source recommendation cache is outside provider clearing.

Build and run on the supported Go toolchain:

```sh
mkdir -p work/commands
go build -buildvcs=false -race -o work/commands/worker ./cmd/owned-clear-cache-fixture
go build -buildvcs=false -race -o work/commands/server ./cmd/server
OWNED_CLEAR_CACHE_BINARY="$PWD/work/commands/worker" \
OWNED_CLEAR_CACHE_SERVER_BINARY="$PWD/work/commands/server" \
go test -race ./internal/app -run '^TestOriginalClearCache' -count=1
```

The process test uses temporary private SQLite/data directories and ordinary
fixture accounts, actual HTTP installation/authentication, independent host and
SDK processes, and a loopback notification recipient. No production service is
contacted. It checks real disk/cache effects, plugin override, failure, caller
cancellation after acceptance, isolation, and cold restart. Native TMDB cache
misses are checked with **no credentials** (`NOT_CONFIGURED`), not represented
as successful external TMDB requests. All other command presets and platform
features remain outside this slice.

The source witness executes whole chain/command/cache components with explicit
unrelated import/registry fixtures and unmodified provider/API clear-method
excerpts. It uses real source memory/file caches; it is **not** a full source
service, authentication integration, Redis acceptance, or external-provider test.
