# Plugin module original-method port: standard methods and services (WIP)

This remains a partial development checkpoint against MoviePilot 8a83efeacd3e26b82be495e17d50146e31c96a60. It is not complete module acceptance. Main and dev are unchanged.

## Included

- The shared manager reads real loaded objects for state, service and command declarations. Additional native adapters retain actual objects across direct init, preserve partial failures, distinguish new/reloaded objects and clones, and keep public service registration separate from private init-created once jobs. Existing typed business parsers retain their own behavior; this is not a claim that every migrated plugin business input matches Python's full value domain.
- External get_api declarations are obtained from the actual loaded worker, with original default authentication and bounded real HTTP dispatch. Real tests cover anonymous denial, method checks, authorized responses, removed/replaced declarations and clone identity.
- Registered public services bind a sealed descriptor, real occurrence/run and actual loaded object. External service and command dispatch execute the declared method on the existing process. AutoSignIn and CleanInvalidSeed public service adapters retain their native execution guards rather than fabricate old private schedule receipts. Owned actual-due tests reach SignIn with no sites and Clean with its expected no-downloader source error.
- New AutoSignIn/Clean objects with absent configuration receive real Init(None) without fabricated configuration rows or I/O authority. Subsequent configured init uses the same actual object through the existing host boundary. This does not claim every repeated absent-config branch is complete.
- Go artifact discovery shares the strict metadata reader with acquisition. Repository origins survive reopen and clone. Ordered market sources use the existing encrypted Settings record. Backend tests verify real discovery, unsupported contracts, metadata-only reads, install, loaded state and reopen. The separate market frontend patch is not included here.
- SDK envelope encoding avoids a redundant outer scan of already standard-library-encoded JSON while preserving validation, escaping, field order and depth limits. Direct immutable legacy-codec equivalence tests cover valid and malformed inputs.

## Exact validation scope

The v47 frozen selected batch passed 57 root tests in ordinary mode and the same 57 in race mode, with exit zero and unchanged input hashes. App package elapsed times were 81.907 and 239.282 seconds. This is a selected standard-method/native-service/market batch, not a fresh full-project regression or a rerun of all earlier lifecycle roots.

The exact SDK codec candidate separately passed all 72 SDK roots in ordinary and race modes. The original 90-second, four-way 16 MiB test passed with both parent and worker race-instrumented; no timeout, payload or assertion was relaxed. Historical full-race failures remain preserved. The two earlier pre-host budget failures were not causally explained by later passes. Artifact v6 separately passed all 23 roots in ordinary and race modes.

The v44 overlay build failure, v45 markets routing failure and v46 native objects incorrectly sent to the external session path are retained as historical failures. The final batch fixes the reserved markets route and dispatches native/external bindings by actual object type without replacing the object or reinitializing it.

## Reproducing owned fixtures

Build cmd/owned-module-fixture for declaration tests and cmd/owned-service-fixture for actual service/command tests. Build version 2.0 with -ldflags '-X main.fixtureVersion=2.0'. The API workers are cmd/original-api-fixture and cmd/original-api-registration-fixture. Tests require their documented OWNED_MODULE_FIXTURE_BINARY, OWNED_MODULE_FIXTURE_BINARY_V2, OWNED_MODULE_FIXTURE_BINARY_V3, OWNED_MODULE_FIXTURE_BINARY_V4, ORIGINAL_API_FIXTURE_BINARY and ORIGINAL_API_REGISTRATION_FIXTURE_BINARY paths. All acquisition servers and business inputs are owned synthetic fixtures.

## Remaining acceptance

- Integrate and verify shared PluginBase configuration/data/path methods and fresh per-call/stop authority. That independent untested layer is intentionally excluded.
- Complete all native public-service execution bindings and continuous normal lifecycle behavior. A stopped Go process cannot execute a retained central callback; this bounded process adaptation is not a claim that Python Scheduler checks the running map before every callback.
- Finish common Form/Page/dashboard/navigation and loaded-but-uninstalled route behavior against the original interfaces.
- Integrate the market frontend with current main UI, then run real normal-server Chrome flows through sources, installation, cancel/save, clone, enable/disable, service/command/event, reload/reset, true version upgrade, ordinary/clone uninstall and recreation.
- Run final full common ordinary/race regressions, web checks/build, vet, Linux build and embedded-asset verification on final integrated bytes. Preserve main's independent search, poster, catalog and UI changes through a normal three-way merge.

No full-module or full-plugin-business acceptance is claimed. Other plugin development resumes only after complete module acceptance, authorized main integration and official readback.
