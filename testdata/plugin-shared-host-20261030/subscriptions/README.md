# S05 independent source fixtures (2026-10-30)

Source: MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`.
`source-oracle.json` is the independent source auditor's unmodified canonical
output (SHA-256 `5177722afe3b90812aacbd862f5efac8fc43a3d0bc1b7f6d389167092f292aa0`).
`source-float-oracle.json` is its additional real FLOAT grammar observation
(SHA-256 `7cda25ab8d108dcfc4d29c4cba0e2dc54e47ee5940e74a4c1e7495fe87ed4d1d`).
The accompanying manifests bind the original raw observations and source files;
absolute paths in those manifests are historical provenance, not runtime inputs.

The source gate is an owned ordinary plugin instance calling normally imported
`SubscribeOper()` with its default SQLAlchemy sessions and real SQLite. It does
**not** execute PluginManager, PluginBase, the source daemon, or external accounts.
The driver actively rejects Python network operations. `python-freeze.txt` records
the independent test-only dependency environment; these are not product Python
dependencies. Product code is Go only.

## Reproduce a source group

Use Python 3.12.14 and install the exact test dependencies in a fresh venv using
`source/python-freeze.txt` and the fixed source constraints. Set `FIXED_SOURCE` to
the readonly fixed source checkout, `FIXTURE` to this directory's `source`, and
`VENV` to that isolated test environment. `OWNED_CONFIG` must be a newly created
empty temporary directory. For example:

```sh
INPUT="$FIXTURE/inputs/raw-columns.json"
OUTPUT="$OWNED_OUTPUT/raw-columns.json"
env -i PATH=/usr/bin:/bin HOME="$OWNED_CONFIG" CONFIG_DIR="$OWNED_CONFIG" \
  PYTHONNOUSERSITE=1 PYTHONDONTWRITEBYTECODE=1 LANG=C.UTF-8 TZ=Asia/Shanghai \
  DB_TYPE=sqlite PYTHONPATH="$FIXED_SOURCE:$FIXTURE" \
  "$VENV/bin/python" "$FIXTURE/source_driver.py" "$INPUT" "$OUTPUT"
```

Run `inputs/cold.json` in a **new Python process with the same CONFIG_DIR** for
cold reads. Run each other group with a distinct fresh CONFIG_DIR. The `*-initial`
inputs capture complete source rows before mutations. Do not compute expected
rows using the Go implementation. Source concurrency and HTTP JSON-boundary
observations have separate drivers in `source/`.

## Target gate and explicit adaptations

```sh
go test ./internal/app -count=1 -run '^TestSharedSubscriptions' -v
OWNED_SHARED_HOST_RACE=1 go test -race ./internal/app -count=1 \
  -run '^TestSharedSubscriptions' -v
```

These tests build a genuine external SDK worker, install its hashed artifact
through the authenticated installer, then call the four typed SDK methods.
The process test additionally builds/runs actual `cmd/server`, uses authenticated
loopback HTTP, stops it with SIGTERM/Wait and cold-starts it with the same DB.
Set `SHARED_SUBSCRIPTIONS_PROCESS_EVIDENCE_DIR` to retain owned server logs.
No site import/export or external account test is included.

The DAO differential checks all complete row columns and list order for the
four ordinary groups (112 main operations + 16 group-end cold reads), plus the
14 additional FLOAT grammar operations and four legal JSON exponent-overflow
operations. The exponent input remains literal `1e400`; observational nonfinite
tags are never used as target input. Source primary keys are remapped to stable host
opaque identities. The carried version-replacement close fixture checks that the exact detached
stop closure still has DAO access after installation generation changes, while
ordinary stale callbacks remain denied.

Source mutable-primary-key cases are **not** called equivalent:
SDK `id` mutation is explicitly rejected and tested for no side effects.
Source `update` returns an expired detached ORM; this SDK returns a fresh persisted
row. Unknown transient ORM attributes are ignored while known columns commit.
The host's optional revision is additional; omitting it retains ordinary updates.

Source FLOAT infinity is committed. The raw-row JSON boundary then fails, including
a cold read; a later finite update recovers. This corresponds to the independently
observed source raw-row route, **not** its Pydantic `schemas.Subscribe` route
(which renders `vote:null`) or an ordinary caller which ignores the ORM return.
Dedicated internal metadata preserves infinity; no public tagged JSON is added.
Unsupported native business calculation shapes retain exact source facts while
native consumers receive their limited typed projection, not a claim of universal
ORM/UI equivalence. NULL vote clears the native stale finite value to zero.

List predicates use the source's observed SQLite indexes without inventing a
business ORDER BY guarantee. Source SQLite is 3.53.1; each target run logs its own
SQLite version. The standard SDK retains its existing 16 MiB logical payload
boundary; no new 128 KiB DAO cap is introduced. The large real-worker gate sends
1,843,237 bytes and verifies full readback and cold persistence.


## Nested JSON numeric supplements

`source-nested-json-oracle.json` / `source-finite-json-oracle.json` contain the
independent source's three overflow/repair operations and finite decimal case.
`source-four-json-oracle.json` binds three four-column updates followed by three
separate source cold processes (get/list/list-by-null in each). The literal
inputs live in `source/inputs/`; the four-column mixed initial SQL snapshot is
kept separately for fixture arrangement. Expectations remain source-produced.

JSON decimal/exponent numbers follow Python IEEE conversion recursively;
integer tokens retain arbitrary precision. Nonfinite nested values are stored
as private source facts, cause raw-row serialization failure only after commit,
remain failures through unrelated writes and cold reload, and recover after
finite/null replacement. Tests compare the internal nested fact to independently
observed source data using test-only observation tags, never a public SDK tag.
