#!/usr/bin/env bash
set -euo pipefail
ROOT=/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305
G="$ROOT/target-drafts/s03-qb-cookie01/gates02"
source /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/env.sh
# Fixed offline toolchain/build controls only. Inherit every proxy/guard value.
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=readonly -p=1 -buildvcs=false'
export GOMAXPROCS=2 CGO_ENABLED=1 TZ=UTC LANG=C.UTF-8
[[ "$(command -v go)" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go ]]
[[ "$GOCACHE" == /workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build ]]
case "${1-}" in
 native-normal)
  label=s03-qb-cookie-native02
  package=./internal/plugins/iyuuautoseed/native
  roots=(TestSharedQBCookieScopeWireAndVersionGuards TestSharedQBCookieScopeAggregateUnknownAndRecovery TestSharedQBCookieScopeGuardsAndLifetime TestSharedQBCookieVersionOriginal23)
  extra=()
  ;;
 native-race)
  label=s03-qb-cookie-native-race02
  package=./internal/plugins/iyuuautoseed/native
  roots=(TestSharedQBCookieScopeGuardsAndLifetime)
  extra=(--race)
  ;;
 *) printf 'Exact mode required: native-normal | native-race\n' >&2; exit 64 ;;
esac
pattern="^($(IFS='|'; echo "${roots[*]}"))$"
expects=()
for root in "${roots[@]}"; do expects+=(--expect "$root"); done
exec python3 "$ROOT/runners/s11-overlay-go-gate03.py" \
 --root "$ROOT" --label "$label" --package "$package" \
 --overlay "$G/overlay.json" --binding "$G/overlay-inputs.json" \
 --extra-input "$ROOT/release-light-media/internal/plugins/iyuuautoseed/reference/native-projection-oracle.json" \
 --run "$pattern" "${expects[@]}" "${extra[@]}"
