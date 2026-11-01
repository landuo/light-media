#!/usr/bin/env bash
set -euo pipefail
ROOT=/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305
G="$ROOT/target-drafts/s01-qb-query-tls01/gates02"
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false' GOMAXPROCS=2 CGO_ENABLED=1
[[ "$(command -v go)" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go ]]
[[ "$GOCACHE" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build ]]
case "${1-}" in
 normal) label=s01-qb-query-tls-native02; extra=() ;;
 race) label=s01-qb-query-tls-native-race02; extra=(--race) ;;
 *) printf 'Exact mode required: normal | race\n' >&2; exit 64 ;;
esac
exec "$ROOT/source-runtime/s10-venv/bin/python3.12" -I -S -B "$ROOT/runners/s11-overlay-go-gate03.py" \
 --root "$ROOT" --label "$label" --package ./internal/plugins/iyuuautoseed/native \
 --overlay "$G/overlay.json" --binding "$G/overlay-inputs.json" \
 --run '^TestQBittorrentCompletedPrivateTLSAndScope$' \
 --expect TestQBittorrentCompletedPrivateTLSAndScope "${extra[@]}"
