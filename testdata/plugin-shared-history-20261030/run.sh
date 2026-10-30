#!/usr/bin/env bash
# Fresh local source-component / actual worker / native SQLite reproduction.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
OUT=${1:?give a fresh owned evidence output directory}
MODE=${2:-normal}
FLAGS=()
if [[ "$MODE" == --race ]]; then FLAGS=(-race); elif [[ "$MODE" != normal ]]; then echo 'mode must be normal or --race' >&2; exit 2; fi
: "${HISTORY_SOURCE_ROOT:?read-only pinned MoviePilot checkout required}"
: "${HISTORY_SOURCE_PYTHON:?compatible source-only Python executable required}"
[[ ! -e "$OUT" ]] || { echo 'refusing to overwrite evidence directory' >&2; exit 2; }
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
export HISTORY_SOURCE_ROOT=$(cd "$HISTORY_SOURCE_ROOT" && pwd)
export HISTORY_SOURCE_PYTHON=$(cd "$(dirname "$HISTORY_SOURCE_PYTHON")" && pwd)/$(basename "$HISTORY_SOURCE_PYTHON")
export HISTORY_SOURCE_DRIVER="$HERE/source_driver_v3.py"
export HISTORY_SOURCE_QUERY_OBSERVATIONS="$HERE/query-observed-03.json"
export HISTORY_PRODUCER_EVIDENCE="$OUT"
export HISTORY_OWNER_CHECKPOINT_EVIDENCE="$OUT"
export OWNED_HISTORY_FIXTURE_BINARY="$OUT/worker"
export OWNED_HISTORY_DENIED_FIXTURE_BINARY="$OUT/worker-denied"
export TZ=Asia/Shanghai PYTHONDONTWRITEBYTECODE=1
python3 - "$HERE/scope.json" "$HISTORY_SOURCE_ROOT" <<'PY'
import hashlib,json,sys
from pathlib import Path
scope=json.loads(Path(sys.argv[1]).read_text());root=Path(sys.argv[2])
for name,digest in scope['source_files_sha256'].items():
 if hashlib.sha256((root/name).read_bytes()).hexdigest()!=digest:raise SystemExit('pinned source file mismatch: '+name)
print('Pinned source component hashes match')
PY
cd "$ROOT"
(go version; go env GOOS GOARCH CGO_ENABLED GOMOD GOTOOLCHAIN) > "$OUT/go-environment.txt"
go build -buildvcs=false "${FLAGS[@]}" -o "$OWNED_HISTORY_FIXTURE_BINARY" ./cmd/owned-history-fixture
go build -buildvcs=false "${FLAGS[@]}" -ldflags '-X main.fixtureDenied=true' -o "$OWNED_HISTORY_DENIED_FIXTURE_BINARY" ./cmd/owned-history-fixture
go test -buildvcs=false "${FLAGS[@]}" -c -o "$OUT/app.test" ./internal/app
sha256sum "$OWNED_HISTORY_FIXTURE_BINARY" "$OWNED_HISTORY_DENIED_FIXTURE_BINARY" "$OUT/app.test" "$HISTORY_SOURCE_DRIVER" "$HISTORY_SOURCE_QUERY_OBSERVATIONS" > "$OUT/runner-sha256.txt"
set +e
(cd internal/app && "$OUT/app.test" -test.count=1 -test.v -test.run '^(TestSharedHistory|TestImport|TestHistoryUpsert)') > "$OUT/app.log" 2>&1
APP=$?
go test -buildvcs=false "${FLAGS[@]}" -count=1 -run '^TestSharedHistory' -v ./internal/catalog ./internal/plugins > "$OUT/catalog-sdk.log" 2>&1
OTHER=$?
set -e
printf 'app=%s catalog_sdk=%s\n' "$APP" "$OTHER" | tee "$OUT/results.txt"
if [[ "$APP" != 0 || "$OTHER" != 0 ]]; then tail -80 "$OUT/app.log"; cat "$OUT/catalog-sdk.log"; exit 1; fi
