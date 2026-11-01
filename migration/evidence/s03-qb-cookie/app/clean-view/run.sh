#!/usr/bin/env bash
set -euo pipefail
ROOT=/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305
C="$ROOT/target-drafts/s03-qb-cookie01/app01/clean-view01"
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false'
export GOMAXPROCS=2 CGO_ENABLED=1 TZ=UTC LANG=C.UTF-8
[[ "$(command -v go)" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go ]]
[[ "$GOCACHE" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build ]]
exec python3 "$ROOT/runners/s11-overlay-go-gate03.py" \
 --root "$ROOT" --label s03-qb-cookie-app01 --package ./internal/app \
 --overlay "$C/overlay.json" --binding "$C/overlay-inputs.json" \
 --fixture ./cmd/owned-downloader-command-fixture --fixture-env OWNED_DOWNLOADER_COMMAND_WORKER \
 --run '^TestSharedQBCookieActualWorkerRecoveryAndCold$' \
 --expect TestSharedQBCookieActualWorkerRecoveryAndCold
