#!/usr/bin/env bash
# Independent exact-wire / real source DAO / real compiled SDK / SQLite review.
# Run from the installed testdata directory, or set S05_TARGET_ROOT when external.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=${S05_TARGET_ROOT:-$(cd "$HERE/../../../../.." && pwd)}
: "${S05_SOURCE_ROOT:?read-only fixed MoviePilot source checkout required}"
: "${S05_SOURCE_PYTHON:?compatible test-only Python executable required}"
OUT=${1:?fresh owned output directory};MODE=${2:-normal};[[ ! -e "$OUT" ]] || { echo refusing-overwrite >&2;exit 2; };mkdir -p "$OUT";OUT=$(cd "$OUT" && pwd)
export S05_SOURCE_ROOT=$(cd "$S05_SOURCE_ROOT" && pwd)
export S05_SOURCE_PYTHON=$(cd "$(dirname "$S05_SOURCE_PYTHON")" && pwd)/$(basename "$S05_SOURCE_PYTHON")
BOOTSTRAP="$ROOT/testdata/plugin-shared-host-20261030/subscriptions/source"
python3 - "$ROOT/testdata/plugin-shared-host-20261030/subscriptions/unicode/manifest.json" "$S05_SOURCE_ROOT" <<'PY'
import json,hashlib,pathlib,sys
m=json.loads(pathlib.Path(sys.argv[1]).read_text());r=pathlib.Path(sys.argv[2])
for p,h in m['source_files'].items():assert hashlib.sha256((r/p).read_bytes()).hexdigest()==h,p
PY
mkdir "$OUT/config" "$OUT/target"
for phase in warm cold repair;do
 env -i PATH=/usr/bin:/bin HOME="$OUT/config" CONFIG_DIR="$OUT/config" PYTHONNOUSERSITE=1 PYTHONDONTWRITEBYTECODE=1 LANG=C.UTF-8 TZ=Asia/Shanghai DB_TYPE=sqlite PYTHONPATH="$S05_SOURCE_ROOT" SOURCE_BOOTSTRAP="$BOOTSTRAP" "$S05_SOURCE_PYTHON" "$HERE/observe_source.py" "$phase" "$HERE/inputs.json" "$OUT/source-$phase.json" > "$OUT/source-$phase.log" 2>&1
 printf '0\n' > "$OUT/source-$phase.exit"
done
python3 - "$ROOT" "$HERE" "$OUT" <<'PY'
import pathlib,sys,json
r,h,o=map(lambda s:pathlib.Path(s).resolve(),sys.argv[1:]);(o/'overlay.json').write_text(json.dumps({'Replace':{str(r/'internal/app/s05_unicode_independent_test.go'):str(h/'reviewer_test.go')}})+'\n')
PY
export GOFLAGS="${GOFLAGS:-} -buildvcs=false -overlay=$OUT/overlay.json" S05_INDEPENDENT_INPUTS="$HERE/inputs.json" S05_INDEPENDENT_SOURCE="$OUT/source-warm.json" S05_INDEPENDENT_EVIDENCE="$OUT/target" OWNED_SHARED_HOST_RACE=0
FLAGS=();if [[ "$MODE" == --race ]];then FLAGS=(-race);export OWNED_SHARED_HOST_RACE=1;elif [[ "$MODE" != normal ]];then exit 2;fi
cd "$ROOT"
go test -buildvcs=false -overlay="$OUT/overlay.json" "${FLAGS[@]}" -c -o "$OUT/review.test" ./internal/app
sha256sum "$OUT/review.test" "$HERE/run.sh" "$HERE/reviewer_test.go" "$HERE/inputs.json" "$HERE/observe_source.py" "$HERE/compare.py" > "$OUT/runner-sha256.txt"
set +e
(cd internal/app;"$OUT/review.test" -test.count=1 -test.v -test.run '^TestS05UnicodeIndependent') > "$OUT/run.log" 2>&1
STATUS=$?
set -e
printf '%s\n' "$STATUS" > "$OUT/run.exit"
python3 "$HERE/compare.py" "$OUT" "$OUT/target" > "$OUT/differential.log" 2>&1
exit "$STATUS"
