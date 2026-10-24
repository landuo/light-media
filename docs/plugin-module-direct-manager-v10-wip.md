# Direct standard manager: second independent checkpoint

This partial replacement checkpoint builds on 7c946e3a9063546e91194ac742d54dc456b28635. It does not complete the plugin module, all legacy-source deletion, frontend integration or business-plugin adaptation. Main is not changed.

## Included

- The plugin HTTP entry routes to the direct manager. External standard methods, views, registered services, commands, events and API calls use the actual retained SDK object rather than the legacy queued-run worker.
- Public services are registered against actual objects and their real trigger deadlines. The selected regression exercises manual service execution and an actual interval occurrence, without manufacturing a due time or a run receipt.
- Actual-object clone, reset and uninstall paths preserve the tested distinction between source and clone data/configuration. Manager empty configuration remains distinct from Base null; save reuses the loaded object.
- Unadapted native implementations are reported unavailable rather than silently falling back to the old builtin worker. The wider historical native/view compatibility layers are not included in this independent input.
- The first file-deletion batch removes seven old production adapter/initialization/stop files and six directly associated old tests. These are traceable Git deletions, not deletion of repository history, stored evidence or user data. Other legacy source still remains.

## Exact validation

The test input contains the exact published parent Go sources plus this replacement slice and its explicit deletions. The same 2,332 effective Go input files retained their SHA256 values before and after execution. The owned Base and standard-method executables were rebuilt from this input.

Three selected App roots passed in ordinary mode and the same three passed with application race instrumentation. Package elapsed times were 2.600 and 12.703 seconds; both commands exited zero, with no failed or skipped roots. The worker executables were ordinary builds, not race-instrumented. These commands used -vet=off; this checkpoint does not claim vet passed.

- TestOriginalDirectManagerStartsWithEmptyConfigWithoutLegacyRun
- TestOriginalDirectManagerCloneResetUninstallActualObjects
- TestOriginalStandardDirectModuleEndToEnd

The continuous owned fixture covers real HTTP installation/save, standard method reads, Form/Page/dashboard routes, direct service/command/event/API calls, independent clone objects, stop order, reset and uninstall. The tests assert zero legacy plugin_runs rows. The name of the end-to-end test does not mean full product or browser acceptance.

testdata/plugin-direct-manager-v10-evidence.json records changed-source and deletion identities, exact selected results, fixture digests and log digests. No results from the wider historical candidate tree substitute for this independent gate.

## Still unfinished

Complete removal of old execution sources and extraction of shared data types/tools, broader negative and genuine version-upgrade cases, full ordinary/race regression, vet, frontend checks/build, normal-server browser workflows and final build/embedded-asset verification remain unfinished. The untested upgrade and additional negative-case layers are not included here. Business-plugin repairs follow completion of the replacement module.
