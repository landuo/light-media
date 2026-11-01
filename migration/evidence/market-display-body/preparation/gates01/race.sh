#!/usr/bin/env bash
set -euo pipefail
ROOT=/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305
G="$ROOT/target-drafts/market-display-body01/gates01"
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false'
export GOMAXPROCS=2 CGO_ENABLED=1
[[ "$(command -v go)" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go ]]
[[ "$GOCACHE" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build ]]
[[ "$#" == 0 ]]
python3 - "$ROOT/evidence/market-display-body-artifacts-normal01/result.json" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['status']=='PASS'
PY
python3 "$ROOT/runners/s11-overlay-go-gate03.py" \
 --root "$ROOT" --label market-display-body-artifacts-race01 --package ./internal/plugins/artifacts \
 --overlay "$G/overlay.json" --binding "$G/overlay-inputs-race.json" \
 --extra-input "$G/race.sh" --extra-input "$G/seal.json" --race \
 --run '^(TestPythonSourcePackageBodyBeyondNativeIndexBudget|TestPythonSourcePackageReaderFailuresAndCancellation|TestPythonSourcePackageRetainsTransportGuards|TestNativeRepositoryAndArtifactBodyBudgetsRemainBounded)$' --expect TestPythonSourcePackageBodyBeyondNativeIndexBudget --expect TestPythonSourcePackageReaderFailuresAndCancellation --expect TestPythonSourcePackageRetainsTransportGuards --expect TestNativeRepositoryAndArtifactBodyBudgetsRemainBounded
