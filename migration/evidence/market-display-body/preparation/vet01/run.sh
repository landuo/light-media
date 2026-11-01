#!/usr/bin/env bash
set -euo pipefail
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false'
export GOMAXPROCS=2 CGO_ENABLED=1
[[ "$#" == 0 ]]
[[ "$GOCACHE" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build ]]
python3 /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/target-drafts/market-display-body01/vet01/run_vet.py
