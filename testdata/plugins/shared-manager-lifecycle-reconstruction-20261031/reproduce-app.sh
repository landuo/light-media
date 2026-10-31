#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "$0")/../../.." && pwd)"
cd "$ROOT"
case "${1:-normal}" in
  normal) flags=() ;;
  race) flags=(-race) ;;
  *) printf 'Usage: %s [normal|race]\n' "$0" >&2; exit 2 ;;
esac
[[ "$(go env GOOS)" == linux ]] || { echo 'This recorded reproduction contract is Linux.' >&2; exit 2; }
[[ "$(go env GOVERSION)" == go1.26.0 ]] || { echo 'Recorded toolchain: Go 1.26.0.' >&2; exit 2; }
[[ -z "${PLUGIN_MARKET:-}" && -z "${LIGHT_MEDIA_MEDIA_ROOTS:-}" ]] || { echo 'Inherited real endpoint/mount override conflicts with owned tests; not rewritten.' >&2; exit 2; }
OUT="$(mktemp -d "${TMPDIR:-/tmp}/light-media-s10-replay.XXXXXX")"
printf 'Owned generated programs (retained): %s\n' "$OUT"
export GOMAXPROCS=2 CGO_ENABLED=1
while read -r variable package; do
  binary="$OUT/${package##*/}"
  go build -mod=readonly -buildvcs=false -p=1 "${flags[@]}" -o "$binary" "$package"
  export "$variable=$binary"
done <<'FIXTURES'
OWNED_MANAGER_FIXTURE_BINARY ./cmd/owned-manager-lifecycle-fixture
OWNED_APP_CONCURRENT_FIXTURE_BINARY ./cmd/owned-app-concurrent-fixture
OWNED_SUBMITTED_FIXTURE_BINARY ./cmd/owned-submitted-fixture
OWNED_INPUT_FIXTURE_BINARY ./cmd/owned-input-fixture
ORIGINAL_API_REGISTRATION_FIXTURE_BINARY ./cmd/original-api-registration-fixture
OWNED_STANDARD_FIXTURE_BINARY ./cmd/owned-standard-fixture
FIXTURES
PATTERN='^(TestSharedManagerRemoveClassOnlySourceScope|TestSharedManagerRemoveActualEmptyAllSource|TestSharedSchedulerActualSDKSourceRegistry|TestSharedSchedulerActualRetainedSelfRemove|TestSharedManagerRuntimeSourceDefaultsAndGuards|TestSharedManagerRuntimeActualConcurrentInitState|TestSharedManagerRuntimeLocalSourceDescriptors|TestSharedManagerMutationsClassForceAndIsolation|TestSharedManagerMutationsConfigFailureHotCold|TestOriginalRetainedOwnersAndExactAdmission|TestOriginalRetainedCallFactsAndForcedClose|TestOriginalRetainedConcurrentRetirementOnce|TestOriginalRetainedActualCancelledCallAndForcedClose|TestOriginalRetainedActualUnpublishedSubmission|TestOriginalAPIRegistrationImmutableSnapshotAndPointer|TestOriginalAPIRegistrationActualRemoveRetainsExactWorker|TestPluginOriginalExternalHTTPRevokedKeyAndUninstalledRoute|TestPluginOriginalExternalHTTPRejectsRemovedRegisteredRoute|TestOriginalServiceGetterFailureReplacesAllRegistrations|TestOriginalServiceActualSDKAsyncReplacementSource|TestOriginalServiceActualSDKSuffixReplacementAndSyncWait|TestOriginalSubmittedActualSynchronousRefreshAndGetterRecursion|TestOriginalRetainedLegacyRemovedLastOwnerTerminatesTransport)$'
go test -mod=readonly -buildvcs=false -p=1 "${flags[@]}" ./internal/app -v -run "$PATTERN" -count=1 -timeout=240s
