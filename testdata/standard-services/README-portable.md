# Portable service acceptance fixtures

These are **development verification tools**, not Python production dependencies.
The Go host and installed SDK worker run natively. Normal Go acceptance consumes
frozen JSON and does not need MoviePilot, Python, a private audit folder or network.
The localhost fixture repository is synthetic and owns all its test data.

## Go acceptance (repository root)

```sh
mkdir -p work/standard-services
go build -o work/standard-services/owned-input-fixture ./cmd/owned-input-fixture
OWNED_INPUT_FIXTURE_BINARY="$PWD/work/standard-services/owned-input-fixture" \
  go test ./internal/app -count=1 -v \
  -run='^TestOriginalService(WholeSourceProgress|GetterFailureReplacesAllRegistrations)$'
```

Both roots must appear and PASS; zero matches or SKIP is not acceptance. The worker
must be built from this checkout, including the synthetic `getter_error` flag.
`OWNED_SERVICE_TARGET_OBSERVATIONS` optionally names an output JSON file; when
unset the whole-source test uses `t.TempDir()`. It does **not** require
`OWNED_SERVICE_SOURCE_OBSERVATIONS`. The old private recovery overlay is not a
prerequisite and must not be selected by the regex above.

`whole-source-progress.json` contains six original source observations, not target
expectations disguised as source. Only timestamps are removed from raw progress.
Empty error `""`, successful error `null`, and integer `9007199254740993` are retained.
`safe_projection` selects stable shared fields. Its sensitive synthetic error alone
applies the already-established host password/token/URL redaction contract;
`raw_source_progress` remains unchanged. Identity/name and language-specific
representation are not falsely claimed equal.

The getter test installs a real SDK process, uses ordinary PUT config, and observes
registration, two-service replacement, getter failure, empty/disabled declarations,
recovery on the same object and a real reload replacement. The fixture addition is
only in `get_service`; `run_service` and the browser fixture are unchanged.

## Replay the original components (optional developer environment)

Use a clean read-only MoviePilot checkout at
`8a83efeacd3e26b82be495e17d50146e31c96a60`. The runner checks git HEAD, tracked changes,
and all 103 imported source-file SHA-256 values in `whole-source-bindings.json`.
No AST extraction, copied scheduler implementation or target oracle generation is
used. It normally imports real Scheduler, PluginManager, ProgressHelper,
ScheduleProgress and MemoryBackend, with actual APScheduler **3.11.2**, cachetools
**7.1.8**, Pydantic **2.14.2** and SQLAlchemy **2.0.55**. Each executing package version
is checked in addition to distribution metadata.

`replay-requirements.txt` locks the observed development dependency environment
(including its transitive packages; it is not a list of Go runtime dependencies).
Python **3.12.14** on Linux was used. Reuse existing packages when available. For a
new environment, install the lock in an isolated environment before replay:

```sh
python3.12 -m venv work/standard-services/venv
work/standard-services/venv/bin/python -m pip install -r testdata/standard-services/replay-requirements.txt
PY=work/standard-services/venv/bin/python
DEPS="$PWD/work/standard-services/venv/lib/python3.12/site-packages"
# Set SOURCE_ROOT to your existing, fixed MoviePilot checkout.
"$PY" testdata/standard-services/replay_whole_source.py \
  --source-root "$SOURCE_ROOT" --dependency-root "$DEPS" \
  --output work/standard-services/source-replay
"$PY" testdata/standard-services/replay_calendar_jitter.py \
  --dependency-root "$DEPS" --output work/standard-services/calendar-replay
```

Dependency acquisition may need network; both replay programs themselves forbid
all socket connections. `--dependency-root` can be repeated; caller order is import
precedence. Output directories must not already exist, preserving older failures.
Reports label paths as SOURCE/DEPENDENCY_n/FIXTURE/PYTHON and include imported-file
hashes, Python/platform and system zoneinfo hashes. No credentials or user database
is required; the source runner creates and removes its own temporary runtime DB.

The source runner retains 15 scenarios (six manual return/error cases plus missing,
clock, removal, suffix/invalid trigger, paused list error, schema, async replacement,
and actual memory TLRU), and checks all six normalized manual observations against
the frozen raw source. Explicit perimeter stubs cover out-of-scope business chains,
watcher, wallpaper and official licensing; failures record notices without sending.
This is **not** a full MoviePilot daemon, authentication, loader or browser run.

The calendar bridge executes real APS 3.11.2 against the existing 3.11.3 calendar
oracle (33 cases) and the existing four jitter cases. It reuses
`generate_jitter_oracle.py`; the deterministic 0.25 uniform draw compares algorithms,
not Python versus Go RNG streams. It advances UTC instants by 1 microsecond so DST
folds are retained, and compares instants while retaining original offset strings.
`calendar-jitter-aps312.json` is the independently observed bridge output; all 33/4
cases are checked again. `portable-provenance.json` binds the original oracle,
source observation and bridge hashes. No dependency packages or binaries are vendored.
