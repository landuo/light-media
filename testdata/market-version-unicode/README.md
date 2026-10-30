# Fixed-source Unicode market-version checks

These are **test-only** Python source witnesses. The Go application and its
workers do not invoke Python.

Source: MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`, observed with
Python 3.12 / Unicode 15.0.0. `source-oracle.json` contains 984 actual source
comparisons, 680 decimal code points, 128 digit-only code points and 29 strip
characters. The Go test also checks all Unicode code-point classifications;
it does not claim exhaustive testing of all strings.

The comparator preserves source dot/hyphen splitting, `vV` stripping, release
weights, zero padding, invalid comparisons and the 4,300-digit integer limit.
Native executable manifest validation is unchanged. Python metadata remains
display-only.

## Reproduce

Use Go 1.26 and the fixed source/dependency environment documented in the sibling
`market-selection-collision/README.md`. Run only the normal HTTP mirror fixture;
no TLS interception, custom CA, CONNECT forwarding or production repository is
needed. The fixture fails if proxy or CA environment variables are inherited.

```sh
export GOFLAGS='-mod=readonly -buildvcs=false'
go test -race -count=1 ./internal/app -run '^TestOriginalMarketVersion'
mkdir -p work/market-qa
go build -o work/market-qa/server ./cmd/server
# SOURCE_PYTHON and MOVIEPILOT_SOURCE must refer to the fixed original environment.
export MARKET_SELECTION_SOURCE_OUTPUT="$PWD/work/market-qa/unicode-source-NEW"
"$SOURCE_PYTHON" testdata/market-version-unicode/source_market.py
"$SOURCE_PYTHON" testdata/market-version-unicode/source_market.py --cold
MARKET_SELECTION_SERVER="$PWD/work/market-qa/server" \
MARKET_SELECTION_SOURCE_RESULT="$MARKET_SELECTION_SOURCE_OUTPUT/warm-result.json" \
MARKET_SELECTION_OUTPUT="$PWD/work/market-qa/unicode-target-NEW" \
python3 testdata/market-version-unicode/production_market.py
```

Use fresh output directories. Preserve the source-owned SQLite directory only
between its warm/cold pair. Do not publish generated account/config/database
files or runtime logs. Forty target observations cover forced and ordinary
queries and an actual second server process. Source authentication and full
original daemon startup are not claimed; the source witness imports complete
Manager/helper/endpoint components and uses declared test-only unused-integration
and administrator fixtures.

Combined current integration, native browser and remaining market limitations
are recorded separately in `migration/ledger.json`; these tests alone do not
close all of market group 09.
