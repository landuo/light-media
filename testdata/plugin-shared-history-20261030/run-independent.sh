#!/usr/bin/env bash
# Reviewer-owned real worker/native SQLite/source-component integration checks.
# Requires Go 1.26 plus a read-only pinned source and test-only Python dependencies.
# No normal application Python dependency; no private audit path is required.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd); ROOT=$(cd "$HERE/../.." && pwd)
OUT=${1:?fresh owned evidence output directory}; MODE=${2:-normal}
FLAGS=(); export OWNED_SHARED_HOST_RACE=0
if [[ "$MODE" == --race ]]; then FLAGS=(-race); export OWNED_SHARED_HOST_RACE=1; elif [[ "$MODE" != normal ]]; then echo 'mode must be normal or --race' >&2; exit 2; fi
: "${HISTORY_SOURCE_ROOT:?read-only pinned MoviePilot checkout required}"
: "${HISTORY_SOURCE_PYTHON:?compatible source-only Python executable required}"
[[ ! -e "$OUT" ]] || { echo 'refusing to overwrite evidence' >&2; exit 2; }
mkdir -p "$OUT"; OUT=$(cd "$OUT" && pwd)
export HISTORY_SOURCE_ROOT=$(cd "$HISTORY_SOURCE_ROOT" && pwd)
export HISTORY_SOURCE_PYTHON=$(cd "$(dirname "$HISTORY_SOURCE_PYTHON")" && pwd)/$(basename "$HISTORY_SOURCE_PYTHON")
export HISTORY_SOURCE_DRIVER="$HERE/source_driver_v3.py"
export HISTORY_INDEPENDENT_QUERY_SOURCE="$OUT/independent-query-source.json"
export HISTORY_INDEPENDENT_EVIDENCE="$OUT"
export OWNED_HISTORY_FIXTURE_BINARY="$OUT/history-worker" HISTORY_INDEPENDENT_UI_BINARY="$OUT/ui-worker"
export GOFLAGS="${GOFLAGS:-} -buildvcs=false" PYTHONDONTWRITEBYTECODE=1 TZ=Asia/Shanghai
python3 - "$HERE/scope.json" "$HISTORY_SOURCE_ROOT" <<'PY'
import hashlib,json,sys
from pathlib import Path
scope=json.loads(Path(sys.argv[1]).read_text());root=Path(sys.argv[2])
for name,digest in scope['source_files_sha256'].items():
 if hashlib.sha256((root/name).read_bytes()).hexdigest()!=digest:raise SystemExit('pinned source mismatch: '+name)
print('Pinned source component hashes match')
PY
mkdir "$OUT/query-source-config"
CONFIG_DIR="$OUT/query-source-config" HOME="$OUT/query-source-config" PYTHONPATH="$HISTORY_SOURCE_ROOT:$HERE" PYTHONNOUSERSITE=1 DB_TYPE=sqlite "$HISTORY_SOURCE_PYTHON" "$HISTORY_SOURCE_DRIVER" "$HERE/independent/query-inputs.json" "$HISTORY_INDEPENDENT_QUERY_SOURCE" > "$OUT/query-source.log" 2>&1
printf '0\n' > "$OUT/query-source.exit"
cd "$ROOT"
(go version; go env GOOS GOARCH CGO_ENABLED GOMOD GOTOOLCHAIN) > "$OUT/go-environment.txt"
# These actual go-list inputs bind host, UI, history and S05 worker build closures.
capture() {
 go list -buildvcs=false -deps -test -json ./internal/app ./cmd/owned-history-fixture ./cmd/owned-history-ui-fixture ./cmd/owned-shared-host-fixture > "$OUT/go-list-$1.json"
 python3 - "$OUT/go-list-$1.json" "$OUT/closure-$1.json" <<'PY'
import hashlib,json,pathlib,sys
raw=pathlib.Path(sys.argv[1]).read_text(); decoder=json.JSONDecoder(); files={}
while raw.strip():
 raw=raw.lstrip();p,n=decoder.raw_decode(raw);raw=raw[n:];d=pathlib.Path(p.get('Dir','/nonexistent'))
 for key in ('GoFiles','CgoFiles','CFiles','HFiles','SFiles','SysoFiles','EmbedFiles','TestGoFiles','XTestGoFiles','TestEmbedFiles','XTestEmbedFiles'):
  for name in p.get(key,[]):
   f=(d/name).resolve()
   if f.is_file():files[str(f)]=hashlib.sha256(f.read_bytes()).hexdigest()
for name in ('go.mod','go.sum'):
 f=pathlib.Path(name).resolve();files[str(f)]=hashlib.sha256(f.read_bytes()).hexdigest()
pathlib.Path(sys.argv[2]).write_text(json.dumps(files,sort_keys=True,indent=2)+'\n')
PY
}
capture before
sha256sum "$HERE/run-independent.sh" "$HERE/independent/query-inputs.json" "$HISTORY_SOURCE_DRIVER" > "$OUT/fixture-sha256.txt"
go build -buildvcs=false "${FLAGS[@]}" -o "$OWNED_HISTORY_FIXTURE_BINARY" ./cmd/owned-history-fixture
go build -buildvcs=false "${FLAGS[@]}" -o "$HISTORY_INDEPENDENT_UI_BINARY" ./cmd/owned-history-ui-fixture
go test -buildvcs=false "${FLAGS[@]}" -c -o "$OUT/review.test" ./internal/app
sha256sum "$OWNED_HISTORY_FIXTURE_BINARY" "$HISTORY_INDEPENDENT_UI_BINARY" "$OUT/review.test" internal/app/history_query_independent_test.go internal/app/history_integration_independent_test.go > "$OUT/runner-sha256.txt"
set +e
(cd internal/app; "$OUT/review.test" -test.count=1 -test.v -test.run '^TestHistory(Independent|IntegrationIndependent)') > "$OUT/run.log" 2>&1
STATUS=$?
set -e
printf '%s\n' "$STATUS" > "$OUT/run.exit"
capture after
cmp "$OUT/closure-before.json" "$OUT/closure-after.json"
printf 'Actual Go build closures unchanged before/after.\n' > "$OUT/binding-integrity.txt"
cat "$OUT/run.log"
exit "$STATUS"
