#!/usr/bin/env bash
# Linux target-side reproduction only. Requires Bash, Go from go.mod, a C
# compiler for -race, and standard coreutils. No MoviePilot/Python installation
# is needed. The committed source observations are not re-executed by this script.
# Usage: ./testdata/shared-plugin-manager/run-independent.sh [checkout-root]
# Optional: SOURCE_ROOT, OUT_ROOT (parent directory), GO, TEST_TIMEOUT.
# Every invocation creates a new output directory; it never reuses old binaries.
set -euo pipefail
umask 077

if (( $# > 1 )); then
  printf 'Usage: %s [checkout-root]\n' "$0" >&2
  exit 2
fi
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
TREE=$(cd -- "${1:-${SOURCE_ROOT:-$SCRIPT_DIR/../..}}" && pwd -P)
GO=${GO:-go}
TEST_TIMEOUT=${TEST_TIMEOUT:-15m}
OUTPUT_PARENT=${OUT_ROOT:-${TMPDIR:-/tmp}}
mkdir -p -- "$OUTPUT_PARENT"
OUTPUT_PARENT=$(cd -- "$OUTPUT_PARENT" && pwd -P)
OUT=$(mktemp -d "$OUTPUT_PARENT/manager-query-independent.XXXXXX")
printf 'Evidence directory: %s\n' "$OUT"
trap 'status=$?; printf "%s\n" "$status" > "$OUT/exit-code"; printf "Exit %s; evidence: %s\n" "$status" "$OUT" >&2' EXIT

cd -- "$TREE"
test -f go.mod
test -f cmd/owned-manager-query-fixture/main.go
[[ "$($GO env GOOS)" == linux ]] || { echo 'This witness requires Linux.' >&2; exit 2; }
[[ "$($GO env CGO_ENABLED)" == 1 ]] || { echo 'CGO_ENABLED=1 is required for -race.' >&2; exit 2; }
# Prevent caller-supplied GOFLAGS from silently removing or changing this run.
export GOFLAGS='-mod=readonly -buildvcs=false'
mkdir -- "$OUT/bin" "$OUT/tmp" "$OUT/process"
"$GO" version > "$OUT/go-version.txt"
"$GO" env -json GOOS GOARCH CGO_ENABLED GOVERSION GOMOD GOWORK > "$OUT/go-env.json"

PACKAGES=(./internal/app ./cmd/owned-manager-query-fixture ./cmd/server ./cmd/owned-base-fixture ./cmd/owned-module-fixture)
capture_inputs() {
  local label=$1 file relative
  "$GO" list -deps -test -json "${PACKAGES[@]}" > "$OUT/dependencies-$label.json"
  "$GO" list -m -json all > "$OUT/modules-$label.json"
  # Record the actual selected local source/test/embed closure, including local
  # module replacements. Module versions/checksums are recorded separately.
  "$GO" list -deps -test -f '{{ $p := . }}{{range .GoFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .CgoFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .CFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .HFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .SFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .SysoFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .EmbedFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .TestGoFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .XTestGoFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .TestEmbedFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}{{range .XTestEmbedFiles}}{{printf "%s/%s\n" $p.Dir .}}{{end}}' "${PACKAGES[@]}" > "$OUT/input-paths-$label.txt"
  {
    printf '%s\n' "$TREE/go.mod" "$TREE/go.sum" "$TREE/testdata/shared-plugin-manager/run-independent.sh"
    printf '%s\n' "$TREE"/testdata/shared-plugin-manager/*.json
    cat "$OUT/input-paths-$label.txt"
  } | LC_ALL=C sort -u | while IFS= read -r file; do
    case "$file" in
      "$TREE"/*)
        relative=${file#"$TREE"/}
        [[ -f "$relative" ]] && sha256sum -- "$relative"
        ;;
    esac
  done > "$OUT/inputs-$label.sha256"
}
capture_inputs before

cat > "$OUT/manager-tests.txt" <<'TESTS'
TestSharedManagerQueriesActualRegistryAndLifecycle
TestSharedManagerQueriesActualConfigSourceAndFailure
TestSharedManagerQueriesActualHTTPAuthConcurrencyAndScopes
TestSharedManagerQueriesProductionProcessCold
TestSharedManagerQueriesWarmCacheOutageValues
TestSharedManagerQueriesCacheNeverHidesInvalidSeal
TestSharedManagerQueriesPrefetchAtomicAndScoped
TestSharedManagerQueriesHTTPFailedSavePublishesConfiguration
TestSharedManagerQueriesHTTPReadFailurePublishesConfiguration
TestSharedManagerQueriesHTTPSameFalseyAndCold
TestSharedManagerQueriesIndependentReadOutageCache
TestSharedManagerQueriesIndependentJSONEdgesAndReadOnly
TestSharedManagerQueriesIndependentFailedClassAndStoppingConfig
TestSharedManagerQueriesIndependentColdUnreadClassOutage
TESTS
cat > "$OUT/base-tests.txt" <<'TESTS'
TestOriginalBaseConfigHTTPUsesCurrentCacheWithoutInitialization
TestOriginalBaseSDKConfigUpdateThenSameCallRead
TestOriginalBaseSDKEmptyCacheReloadResetAndFreshStop
TestOriginalBaseSDKResetSecondFailureStillInvalidatesConfigCache
TestOriginalBaseConfigObservesExternalWrites
TestOriginalBaseFailedWriteRetainsCacheUntilExternalChange
TESTS

build_query() {
  local output=$1 id=$2 order=$3 state=$4 failure=$5 observations=$6 capabilities=$7
  "$GO" build -race -ldflags "-X main.pluginID=$id -X main.pluginOrder=$order -X main.defaultState=$state -X main.failInit=$failure -X main.saveObservations=$observations -X main.queryCapabilities=$capabilities" -o "$OUT/bin/$output" ./cmd/owned-manager-query-fixture
}
CAPS=plugin.manager.get_plugin_ids,plugin.manager.get_running_plugin_ids,plugin.manager.get_plugin_config
build_query Alpha OwnedAlpha 20 true false true "$CAPS"
build_query Beta OwnedBeta 10 false false true "$CAPS"
build_query BetaReadOnly OwnedBeta 10 false false false "$CAPS"
build_query Broken OwnedBroken 30 false true true "$CAPS"
build_query Denied OwnedDenied 20 true false true ''
"$GO" build -race -o "$OUT/bin/server" ./cmd/server
"$GO" build -race -o "$OUT/bin/base" ./cmd/owned-base-fixture
"$GO" build -race -o "$OUT/bin/module" ./cmd/owned-module-fixture
"$GO" test -race -c -o "$OUT/bin/app.test" ./internal/app
for binary in "$OUT"/bin/*; do "$GO" version -m "$binary"; done > "$OUT/binary-buildinfo.txt"
(cd -- "$OUT"; sha256sum bin/*) > "$OUT/binaries.sha256"

export OWNED_MANAGER_ALPHA_BINARY=$OUT/bin/Alpha
export OWNED_MANAGER_BETA_BINARY=$OUT/bin/Beta
export OWNED_MANAGER_BETA_READONLY_BINARY=$OUT/bin/BetaReadOnly
export OWNED_MANAGER_BROKEN_BINARY=$OUT/bin/Broken
export OWNED_MANAGER_DENIED_BINARY=$OUT/bin/Denied
export OWNED_MANAGER_SERVER_BINARY=$OUT/bin/server
export OWNED_MANAGER_PROCESS_EVIDENCE=$OUT/process
export OWNED_BASE_FIXTURE_BINARY=$OUT/bin/base
export OWNED_MODULE_FIXTURE_BINARY=$OUT/bin/module
export TMPDIR=$OUT/tmp
run_suite() {
  local label=$1 expected=$2 expression count name
  expression="^($(paste -sd '|' "$OUT/$label-tests.txt"))$"
  (cd -- "$TREE/internal/app"; "$OUT/bin/app.test" -test.run "$expression" -test.v -test.count=1 -test.timeout "$TEST_TIMEOUT") 2>&1 | tee "$OUT/$label.log"
  if grep -Eq -- '--- (SKIP|FAIL):|WARNING: DATA RACE' "$OUT/$label.log"; then
    echo "$label: skips, failures or races are not accepted" >&2; return 1
  fi
  count=$(grep -c '^--- PASS: ' "$OUT/$label.log")
  [[ "$count" == "$expected" ]] || { echo "$label: expected $expected top-level passes, got $count" >&2; return 1; }
  while IFS= read -r name; do
    grep -Fq -- "--- PASS: $name (" "$OUT/$label.log" || return 1
  done < "$OUT/$label-tests.txt"
}
run_suite manager 14
run_suite base 6
"$GO" vet ./internal/app ./internal/plugins ./cmd/owned-manager-query-fixture ./cmd/server > "$OUT/vet.log" 2>&1
capture_inputs after
cmp "$OUT/inputs-before.sha256" "$OUT/inputs-after.sha256"
cmp "$OUT/modules-before.json" "$OUT/modules-after.json"
printf 'PASS: 14 Manager queries + 6 critical Base regressions; -race, no skips; vet; unchanged local input closure.\n' | tee "$OUT/result.txt"
# Outputs may include synthetic authenticated-test observations. Keep the
# directory private; publish only reviewed summaries, not the raw directory.
(cd -- "$OUT"; find . -type f ! -name checksums.sha256 ! -name exit-code -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) > "$OUT/checksums.sha256"
