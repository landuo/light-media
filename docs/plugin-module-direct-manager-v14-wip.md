# Direct manager: upgrade, failure boundaries and legacy route retirement

This is a partial checkpoint on parent 3360714340500455bb0ac2b2f7ff0ef7ff88b473. It is not complete module acceptance or completion of all legacy framework removal. Main is unchanged, and business-plugin adaptation remains later work.

## Included changes

- Extend the owned standard worker and regressions to use actual independently built 1.0 and 2.0 executable artifacts. The tested source upgrade preserves the clone's older object/version rather than relabeling the same executable.
- Exercise force-download failure followed by missing-source reload, save/init failure with retained partial object state, and close failure with skipped service cleanup but a reaped process.
- Remove the old SQL-backed service/provider/run dispatch bodies and native custom-API tail. The direct standard manager remains the active route, with existing public-service authentication and cross-site GET protections retained.
- Remove seven retired HTTP/form routing functions: pluginAPI, pluginAPIWithoutResetBarrier, pluginResetHTTP, transferAPIWithBarrier, pluginAPIWithoutIYUUBarrier, pluginForm and pluginFormWithOrigin.
- Remove their retired native-adapter direct-call test branches and the obsolete provider-host manifest test file. These removed tests are not reported as passing or as evidence that old business adapters have been repaired. Source algorithms and their remaining tests are not replaced with stubs.

## Actual independent gate

The exact parent-based input, including the route removals, passed the same nine selected App roots in ordinary and race modes. All 2,333 effective Go input hashes remained unchanged. Both commands exited zero; no selected root failed or skipped. Package times were 4.988 seconds ordinary and 23.417 seconds race.

Coverage comprises the three earlier direct-manager lifecycle/standard-contract roots, four real upgrade/failure-boundary roots, the existing public-service authentication/cross-site GET guard and original service time-unit regression. Legacy plugin_runs rows remain zero in the owned lifecycle cases.

The Base worker and distinct standard 1.0/2.0 workers were rebuilt from this same input. Workers were ordinary builds; race instrumentation applied to the App tests. The commands used -vet=off, so this is not a vet result. Exact root results, source/deletion identities, retired function names and fixture/log digests are in testdata/plugin-direct-manager-v14-evidence.json.

## Remaining limits

Other old execution/storage references and legacy source remain to be removed or reduced to shared data utilities. Full ordinary/race regression, broader negative cases, frontend/build checks, normal-server browser workflows, vet and exact embedded-asset verification remain unfinished. No later untested core-deletion draft is included in this checkpoint. Git history, prior evidence and user data are retained.
