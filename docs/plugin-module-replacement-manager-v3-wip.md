# Original-manager replacement: first independent checkpoint

This checkpoint starts replacing the old plugin execution framework with the MoviePilot-style manager. It is not complete module acceptance or completion of legacy-source deletion. Its parent is 0d460e59d5bbbcdb9bc7f67707202cca2285b9e9; no main integration is included.

## Actual changes

- Initialization calls the actual loaded external SDK object's init method directly. Saving configuration calls that same object again, without creating a legacy queued run or using a completion receipt as an initialization prerequisite.
- Manager configuration is distinct from Base configuration: missing/falsey configuration yields an empty object for the manager without manufacturing a database row. The Base read can still return null.
- Native implementations that have not been adapted are not silently executed through the old initializer. They remain unavailable for this new manager stage; business-plugin repair follows module development.
- App startup no longer starts the old plugin run, event, notice or schedule workers, performs queued-run recovery, or restores the old cleanup watchers. The old App shutdown calls for those native owners are removed. The shared application's unrelated scheduler remains.
- The old queued initializeOriginalPlugin implementation is removed from the lifecycle source. The new stop path calls the actual object's SDK stop rather than waiting on a legacy run transition.

## Independent validation

The validation input restores every Go source file to the exact remote parent, then applies only these four source/test changes. Candidate-only Go files and later native/view compatibility layers are excluded. go.mod, go.sum and the existing owned Base fixture source match the parent. The Base worker was rebuilt from this input.

TestOriginalDirectManagerStartsWithEmptyConfigWithoutLegacyRun passed once in ordinary mode and once with the application race-instrumented, with both commands exiting zero. It exercises real HTTP installation and save, the persistent SDK object, manager empty-object versus Base null behavior, same-object reuse and absence of legacy run rows. Root elapsed times were 0.28 and 2.03 seconds; package times were 0.447 and 3.546 seconds. Input hashes remained unchanged. This is one selected regression per mode, not full regression coverage or a browser acceptance run.

The exact source and test-result digests are recorded in testdata/plugin-replacement-manager-v3-evidence.json. The wider historical candidate-tree results are not substituted for this independent gate.

## Remaining work and limitations

Legacy framework source files, native runtime fields and database structures still exist. No source file is deleted by this checkpoint; historical commits, test evidence and user data are preserved. It must not be reported as complete removal of the old framework.

Direct scheduler/event/command/API integration, pure storage extraction, removal of the remaining old execution sources, frontend integration, normal-server browser flows and full module verification remain future stages. Existing earlier checkpoint documents describe historical work, not acceptance of the new replacement architecture.
