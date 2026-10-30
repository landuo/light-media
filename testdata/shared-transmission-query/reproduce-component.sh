#!/usr/bin/env bash
# 完整 leaf/component 差分，不执行原父包或普通业务插件初始化。
set -euo pipefail
: "${MOVIEPILOT_SOURCE:?fixed source checkout required}"
: "${DOWNLOADER_TEST_TOOLS:?owned isolated-daemons directory required}"
: "${TRANSMISSION_QUERY_PYTHONPATH:?existing transmission-rpc 7.0.11 dependency directory required}"
: "${TRANSMISSION_COMPONENT_OUTPUT:?private evidence output directory required}"
RUN_ID=${1:?unique owned instance name required}
PHASE=${2:-paused}
case "$PHASE" in paused|active|queued) ;; *) echo 'phase must be paused, active or queued' >&2; exit 2;; esac
HERE=$(cd -- "$(dirname -- "$0")" && pwd)
# Invoke from the target repository root using its selected Go toolchain.
mkdir -p "$TRANSMISSION_COMPONENT_OUTPUT/bin"
go build -buildvcs=false -race -o "$TRANSMISSION_COMPONENT_OUTPUT/bin/allowed" ./cmd/owned-transmission-query-fixture
go build -buildvcs=false -race -ldflags='-X main.fixtureDenied=true' -o "$TRANSMISSION_COMPONENT_OUTPUT/bin/denied" ./cmd/owned-transmission-query-fixture
export OWNED_TRANSMISSION_QUERY_FIXTURE_BINARY="$TRANSMISSION_COMPONENT_OUTPUT/bin/allowed"
export OWNED_TRANSMISSION_QUERY_DENIED_FIXTURE_BINARY="$TRANSMISSION_COMPONENT_OUTPUT/bin/denied"
export PYTHONPATH="$TRANSMISSION_QUERY_PYTHONPATH" PYTHONDONTWRITEBYTECODE=1 INDEPENDENT_PHASE="$PHASE"
"$DOWNLOADER_TEST_TOOLS/bin/isolated-daemons" run --service transmission --instance "$RUN_ID" -- \
  python3 "$HERE/source_component.py" "$TRANSMISSION_COMPONENT_OUTPUT/$RUN_ID-source.json" \
  go test -race ./internal/plugins ./internal/app -run '^(TestTransmissionQuery|TestSharedTransmissionQueryRealSourceWorker$)' -count=1 -v
