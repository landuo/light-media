# Uninstall form process-fixture milestone

This is a backend test-fixture improvement, not completion of the unified plugin lifecycle framework.

The original install, self-label, stale/fake-preview, uninstall and reinstall assertions remain in a shared helper. The existing test now starts a small **real PluginUnInstall SDK** executable. A second always-run test repeats the same assertions through the actual normal **cmd/server --plugin-worker** entry, hosted by the test App over owned loopback HTTP. It is not a mock, a standalone server-daemon test, or browser acceptance. The real shared Transfer server test is retained in the regression set.

Only four test/fixture files change. The existing shared process builder is parameterized by SDK target/name; toolchain, build-info, race/tag/CGO and executable checks remain. Ordinary SDK tests use an ordinary worker; race SDK tests use a race worker. The production-server worker stays ordinary in both layers. No runtime or product deadline is changed.

## Verified frozen input

On the published 2310034 backend plus these four fixture files, all 32 top-level tests / 54 tests including subtests passed both ordinary and race runs. All 5,431 available frozen input files matched before and after; no selected root was omitted. The exact roots, source changes and result times are in the companion JSON. Product Form remains limited to 10 seconds; the package watchdog is 6 minutes, including nested real executable builds.

The candidate retains unpublished UI assets, which are not part of this commit. Validation exercises backend worker mode only. The aggregate branch must rebuild and revalidate its final combined tree; these results are not UI acceptance or a claim that a published release binary has the same artifact hash.

## Retained failure and limits

The original V5 B run failed TestPluginUninstallHTTPFreshInstallAndReinstallPreviewKeepsExistingFormsPinned with PLUGIN_FORM_FAILED after 10,504 ms using the full App race-test binary as its SDK child. Its red evidence is retained. That failure had no Uninstall-specific phase observation. One bounded later phase-instrumented run passed, so the historical cause is not proved. These faithful process layers replace an oversized test entry without weakening source behavior assertions or retrying until the old run turns green.

Unified reset/update production integration, the pending frontend publication, and later framework stages are not completed by this milestone.
