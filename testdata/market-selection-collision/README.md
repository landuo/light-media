# Market package cache, selector and raw display evidence

This directory contains test-only source witnesses and finite input vectors.
The product is Go-only; no Python production dependency is introduced.

## Contract and scope

Fixed MoviePilot source: `8a83efeacd3e26b82be495e17d50146e31c96a60`.
The slice covers:

- Independent `package.v2.json` / `package.json` success caches, failed force
  reads, ordinary-cache reuse, TTL and capacity eviction order.
- Original package/repository ordering, delimiter-free ID/version collisions,
  highest-version replacement, and local-candidate distinction.
- Six raw display fields (name, description, icon, version, author, history),
  null/structured/numeric values, duplicate JSON keys, history/release views,
  and serialization failures. Executable manifests remain separately validated.
- Unicode version comparison; see `../market-version-unicode/README.md`.

The normal mirror uses the product's existing `github_proxy`/`GITHUB_PROXY`
setting. It serves only finite synthetic `Owned` paths on localhost HTTP. It
never forwards traffic, changes trust, supplies a CA or establishes a CONNECT
connection. Original source authentication/full daemon startup is not claimed:
complete Manager/helper/endpoint/cache modules use declared unused-integration
and administrator test fixtures.

## Evidence origin

The seven compact oracle JSON files were reprojected from newly executed normal
HTTP-mirror source-v3 observations, not target output. The 400 generated selector
cases are independently source-derived. The older pure `source-cases.json`
selection vector is retained as a finite component input; historical execution
hashes inside it are not current network acceptance.

Only the explicit reviewed publication whitelist is delivered. Old TLS fixture
files and raw historical results present in a coordinator's dirty recovery
workspace are **not** part of the delivered fixture or authorized reproduction.
Do not execute them. Source and target secrets/config/DB/binaries are private
runtime artifacts, not evidence for publication.

## Component and real worker checks

From the repository root, with Go 1.26 and existing dependencies:

```sh
export GOFLAGS='-mod=readonly -buildvcs=false'
mkdir -p work/market-qa
# These are real distinct compiled SDK workers, not mocked manifests.
go build -race -o work/market-qa/standard-v1 ./cmd/owned-standard-fixture
go build -race -ldflags '-X main.fixtureVersion=2.0' \
  -o work/market-qa/standard-v2 ./cmd/owned-standard-fixture
export OWNED_STANDARD_FIXTURE_BINARY="$PWD/work/market-qa/standard-v1"
export OWNED_STANDARD_FIXTURE_V2_BINARY="$PWD/work/market-qa/standard-v2"
go test -race -count=1 ./internal/app \
  -run '^(TestOriginalMarketVersion|TestOriginalMarketSelection|TestOriginalMarketPackageCache|TestOriginalMarketSourcePackage|TestOriginalMarketLocalCandidates|TestOriginalMarketHTTPHighestVersion|TestOriginalMarketConcurrentRefresh|TestOriginalMarketHistoryPreserves)'
go test -race -count=1 ./internal/plugins/artifacts \
  -run '^(TestPythonDisplay|TestSourcePackageReader)'
```

## Original/Go production HTTP comparison

Use a clean process environment without `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`
(or lowercase forms), `SSL_CERT_FILE`, `SSL_CERT_DIR`, `REQUESTS_CA_BUNDLE` or
`CURL_CA_BUNDLE`. The fixtures assert that boundary; do not alter trust to make a
run pass. Existing external network policy remains in force.

Set `MOVIEPILOT_SOURCE` to the fixed source and `SOURCE_PYTHON`/`PYTHONPATH` to its
previously verified isolated Python 3.12 dependency environment. The original
modules require their ordinary dependencies, including SQLAlchemy, httpx and
cachetools; a missing dependency is an unexecuted source gate, not parity.

```sh
export MARKET_SELECTION_SOURCE_OUTPUT="$PWD/work/market-qa/fields-source-NEW"
"$SOURCE_PYTHON" testdata/market-selection-collision/half-package/source_wire_cold.py
"$SOURCE_PYTHON" testdata/market-selection-collision/half-package/source_wire_cold.py --cold
go build -o work/market-qa/server ./cmd/server
MARKET_SELECTION_SERVER="$PWD/work/market-qa/server" \
MARKET_SELECTION_SOURCE_RESULT="$MARKET_SELECTION_SOURCE_OUTPUT/warm-result.json" \
MARKET_SELECTION_OUTPUT="$PWD/work/market-qa/fields-target-NEW" \
python3 testdata/market-selection-collision/half-package/production_wire_cold.py
```

Parallel driver pairs cover cache edges, history, surrogate selector keys and
independent numbers/surrogate/duplicate families. Each needs its own fresh output
directory and matching source result. The `production_upstream.py` driver also
requires actual `OwnedA1`/`OwnedA` workers through `MARKET_SELECTION_BIN_DIR`;
its install/cold behavior is separate from display-only tests.

## Limits and decisions

Unserializable source metadata returns the original HTTP500 plain text
`Internal Server Error`. The target does not substitute a JSON envelope.
The history and surrogate-selector runners compare both status and actual error
body, and the native frontend accepts this failed text response without treating
it as a successful or empty market. Prior JSON-envelope differences remain in
historical evidence; the current ledger records the instruction to align source.

Native executable identifiers/indexes retain their separate strict validation;
explicit Python package display supports source dictionary keys and types.
The existing 2MiB body budget still differs from unbounded source reads and is
not approved as exact equivalence. Other market branches remain separately tracked. This finite slice does not close all
of group 09, the full plugin platform, or paused business-plugin migration.
