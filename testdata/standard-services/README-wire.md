# Fixed service HTTP wire acceptance

These fixtures are development-only. Neither Python nor MoviePilot is needed by
production, the Go host, the SDK worker, or ordinary Go tests.

`wire-source-oracle.json` contains **66 actual HTTP observations** from fixed
MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`. The runner imports complete
`dashboard` and `system` endpoint modules and executes the actual `create_app()` /
`V2ResponseMiddleware`, Scheduler, PluginManager, ProgressHelper, schemas,
MemoryBackend and APScheduler 3.11.2. It does not extract endpoint bodies or
substitute scheduler/response algorithms. The 115 imported source files are bound
in `wire-source-bindings.json`; `replay_support.py` also verifies the existing
whole-source bindings, clean fixed checkout and all 95 development package pins.

The input fixture's callback adds `owned: 9007199254740993` and exact `actual_input`
containing mode/progress/gate/text, matching the existing real Go SDK fixture.
Source callback name is `Owned manual`; the Go test uses the same name. This is
explicit fixture instrumentation, not a change to source scheduler/progress code.
Older independent 22/42/66 raw evidence without `actual_input` remains retained.

## Run ordinary acceptance

From the repository root:

```sh
mkdir -p work/standard-services
# Build from the checkout under test, not a previous test artifact.
go build -o work/standard-services/owned-input-fixture ./cmd/owned-input-fixture
OWNED_INPUT_FIXTURE_BINARY="$PWD/work/standard-services/owned-input-fixture" \
  go test ./internal/app -count=1 -v \
  -run='^TestOriginalServiceWire(Source66|SourceQueries20|RoutingSafety)$'
```

All three roots must PASS without SKIP. `Source66` checks all source observations using
actual App.Handler and an installed real SDK worker: status, media type, Allow,
redirects, JSON fields/nulls/envelopes and exact non-JSON bytes. Arbitrary-precision
JSON numbers are compared as rational numbers, never through float64. Only source
fixture identity/provider names and non-null execution timestamps are normalized;
no JSON field or arbitrary plugin extension data is dropped. Timestamp keys/nulls
remain present, and non-null values must be valid source-format timestamps.

Token routes use the real generated application API token. Redirect comparisons
verify it survived unchanged, then omit only that test-added token from persisted
observations (source auth is overridden and has no token query). Normal production
redirects retain RawQuery bytes unchanged. `OWNED_SERVICE_WIRE_OBSERVATIONS` may
name a private output file; no machine-specific output directory is required.

`wire-query-source-oracle.json` preserves 20 independently observed ASGI cases:
12 redirects retaining escaped/repeated query bytes and eight last-jobid cases.
`SourceQueries20` checks their complete wire, including the literal `%2F`, `%2B`,
Unicode escapes, plus signs and duplicate parameters.

`RoutingSafety` additionally checks raw repeated/encoded query preservation, HEAD /
OPTIONS / PUT / DELETE rejection, authentication, cross-site manual denial, last
jobid precedence, and unchanged native/unversioned host-envelope extensions.
Unversioned `/api/dashboard/...` and `/api/system/...` are pre-existing host aliases,
not claimed fixed-source paths; `/api/plugins/services` remains native.

## Reproduce original source HTTP

Use the fixed read-only checkout and existing dependency environment described in
`README-portable.md`. Do not install into the application runtime. Example:

```sh
"$PY" testdata/standard-services/replay_wire_source.py \
  --source-root "$SOURCE_ROOT" --dependency-root "$DEPS" \
  --output work/standard-services/source-http-replay
```

`--dependency-root` can be repeated in precedence order. The source runner refuses
existing output directories, disables all socket connections, uses its own
temporary SQLite configuration, and cleans that owned runtime after execution.
It verifies all 142 observations against the frozen 66-case source oracle,
independently collected 20-case query and 56-case boundary oracles, preserving
raw bodies and SHA-256 values while comparing only valid execution timestamps
with a stable placeholder. Executing FastAPI/Starlette/httpx versions are checked
alongside the already-checked Scheduler/model/cache dependencies; provenance
records all imported file hashes under portable root labels.

Auth dependencies are overridden with an owned administrator to isolate the wire
contract. Full source daemon startup/lifespan, unrelated chains, network integrations
and physical plugin loading are not executed. These are explicit boundaries, not
claims of source authentication/full-daemon equivalence. The Go tests retain actual
host token/cookie/origin enforcement; no auth error behavior is weakened to imitate
the overridden source boundary.

## Wire contract

- v1 lists are bare arrays; v2 lists are `{success:true,message:"",data:[...]}`.
- List rows contain the source 14 fields, including full `progress_detail`, not
  native instance/service identities or `can_run`.
- Manual/progress responses retain source Response's four fields. An accepted
  manual request is independent of its callback's failed business outcome.
- Missing jobid uses exact source v1 detail / v2 error envelope and 422.
- Unsupported methods use 405 and `Allow: GET`; trailing slashes use 307, empty
  body, no Content-Type and a same-origin Location without the trailing slash.
- Paused mixed-clock sorting and invalid progress models return exactly
  `Internal Server Error`, without newline, with `text/plain; charset=utf-8`.
- The native homepage reads `/plugins/services`; remote-plugin source service
  adapters preserve v1/v2 raw wire. Unrelated `system/progress` is unchanged.

The independently discovered CalendarInterval/combining-trigger gap is outside
this HTTP repair. These tests do not certify the entire scheduler feature closed.

## Raw query / repeated-slash regression

`wire-boundary-source-oracle.json` preserves the independent actual ASGI56:
32 malformed/semicolon jobid queries, 12 repeated-slash redirects, and 12 HEAD
responses. `TestPluginServicesProductionCLI` replays all56 through the real server
binary/TCP listener and adds52 actual-session/token/origin/native checks (108 total).
Run with `PLUGIN_PROCESS_SERVER_BINARY` and `OWNED_INPUT_PROCESS_FIXTURE_BINARY`
pointing to newly built server and SDK executables. Repeat with `-race` binaries.
This intentionally does not substitute direct handler calls for ServeMux/HTTP.
`OWNED_SERVICE_WIRE_BOUNDARY_OBSERVATIONS` optionally persists sanitized observations;
only the generated test token is replaced with `<owned-token>` in request/Location.
Real daemon built-in services remain present; native rows are not asserted empty.

`wire-query-decoding-source-oracle.json` contains38 actual fixed Starlette
`QueryParams(bytes)` observations: blank/presence, repeated last scalar, escaped
keys, `+`, semicolon, malformed percent escapes and Python UTF-8 replacement
(including truncated sequences and Latin-1 raw bytes). The portable source runner
revalidates these using the imported real QueryParams. The Go
`TestOriginalServiceWireQueryDecoding` consumes the observations directly.
The tolerant decoder is used only for versioned service `jobid`; token authority,
global URL parsing and native/unversioned APIs deliberately retain existing rules.
Known normal service paths now enter their writer after existing origin/session
checks but before ServeMux canonicalization. No global mux behavior is replaced.
