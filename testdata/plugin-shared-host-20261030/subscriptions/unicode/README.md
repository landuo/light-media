# S05 Unicode JSON follow-up

This fixture exercises the fixed MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`
ordinary `SubscribeOper` with its real SQLAlchemy sessions and SQLite. Source
`warm`, `cold` and `recover` observations were made in three separate Python
processes against one newly created owned database. No source Manager, daemon,
external account or production endpoint is claimed tested.

The four JSON columns (`note`, `sites`, `filter_groups`, `episode_priority`)
commit isolated surrogate codepoints in values and dictionary keys. Their raw-row
`JSONResponse` then fails UTF-8 encoding. A later legal update repairs them.
`U+D800`, `U+DC00`, and a real replacement character `U+FFFD` stay distinct;
valid surrogate pairs combine, decoded duplicate dictionary keys keep the last
value, and arbitrary integers and existing IEEE/nonfinite semantics remain.
Nonfinite floats fail JSON serialization before the completed string is UTF-8
encoded. The `$observation_nonfinite` value in these observations is test-only,
never target input or an SDK output.

In contrast, isolated surrogates in SQL scalar strings fail SQLite binding and
roll back the entire update, including otherwise-valid JSON/native columns.
Unknown transient attribute names/values do not cause a blanket rejection.
The Go SDK reports finite classified operation errors, not Python exceptions.
The already documented opaque identity, fresh update-row, revision and limited
native-projection adaptations still apply. This does not claim every source
HTTP route, native consumer, or malformed byte sequence is equivalent.

## Reproduce source observations

Use the test-only Python environment described by `../source/python-freeze.txt`
and `../source/source-constraints.txt`. `FIXED_SOURCE` must be the read-only pinned
source checkout; `VENV` its test environment; `FIXTURE` this `unicode` directory.
Create fresh empty `OWNED_CONFIG` and `OWNED_OUTPUT` directories. Keep the config
private: ordinary source startup can generate secrets there. Outputs below contain
only synthetic rows and sanitized exception classifications, not configuration.

```sh
for phase in warm cold recover; do
  env -i PATH=/usr/bin:/bin HOME="$OWNED_CONFIG" CONFIG_DIR="$OWNED_CONFIG" \
    PYTHONNOUSERSITE=1 PYTHONDONTWRITEBYTECODE=1 LANG=C.UTF-8 TZ=Asia/Shanghai \
    DB_TYPE=sqlite PYTHONPATH="$FIXED_SOURCE" \
    "$VENV/bin/python" "$FIXTURE/observe_source.py" "$phase" \
    "$OWNED_OUTPUT/source-$phase.json"
done
```

The driver normally imports the neighboring committed `source/source_driver.py`
and its ordinary plugin; no DAO/session monkeypatch is used. That bootstrap
rejects Python network operations (not a syscall-level sandbox). JSONResponse is
the actual installed Starlette component used by FastAPI, not a full HTTP/auth
source-server run. The driver refuses existing output files. Public observations
contain identical facts to the private source run, without absolute import paths.

## Reproduce target gates

From the repository root, with its declared Go toolchain:

```sh
GOFLAGS=-buildvcs=false go test -count=1 ./internal/app \
  -run '^TestSharedSubscriptions' -v
GOFLAGS=-buildvcs=false OWNED_SHARED_HOST_RACE=1 \
  go test -race -count=1 ./internal/app -run '^TestSharedSubscriptions' -v
```

By default the new tests read these portable source observations. Set
`S05_UNICODE_SOURCE_EVIDENCE="$OWNED_OUTPUT"` to use fresh source observations.
Optionally set `S05_UNICODE_EVIDENCE` to a new owned empty directory to retain
literal request bytes. The real external worker receives a `json.RawMessage`
envelope without decode/re-encode cleanup: this matters, because a generic Go
`any` test helper would replace surrogates **before** the host saw the request
and would not prove a host defect. Existing 13 S05 regression tests are unchanged.
New tests cover committed-response failures, real-worker cold recovery, scalar
rollback, ordinary repair, native stale/ABA writes and CAS. Existing process gates
also execute `cmd/server`; no new claim of source full-server parity is made.
