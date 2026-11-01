#!/usr/bin/env bash
set -euo pipefail
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false' GOMAXPROCS=2 CGO_ENABLED=1
[[ "$(command -v go)" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go ]]
exec /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/source-runtime/s10-venv/bin/python3.12 -I -S -B /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/target-drafts/s01-qb-query-tls01/gates03-build/build.py
