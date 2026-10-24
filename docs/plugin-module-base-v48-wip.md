# Plugin Base contract: v48 development checkpoint

This is a partial checkpoint on top of b920eabefc0cb852ae328b2202bb072f79dbd5c8, not complete plugin-module acceptance. The earlier standard-method checkpoint remains historical context. No main integration is part of this checkpoint.

## Included

- The shared Base vocabulary covers configuration reads and updates, data paths, and keyed/all-data save, read and deletion. The SDK helper uses the current invocation's host caller; declaring a method alone does not grant authorization.
- The explicit light-media.sdk-base/v1 contract binds calls to the real loaded instance. Each exchange has fresh authority; a closed callback, replaced generation or detached object cannot borrow ordinary call authority. Stop uses a fresh cleanup phase bound to the exact detached client.
- Base storage and cache handling includes explicit namespaces, clone defaults, unknown namespaces, JSON value distinctions, external configuration changes, reset invalidation and namespace migration. Data-path checks reject unsafe aliases.
- A cached empty object after a falsy configuration update remains distinct from a missing database row; reload reads that cache, while reset invalidates it. An explicit plugin_id selects the requested namespace, including an unknown namespace where schema support permits it, rather than implicitly restricting every operation to the clone's default namespace. This is the recognized Base contract's behavior, not a new per-call permission grant.
- Filesystem data paths intentionally enforce containment and reject unsafe or aliased paths. These safety constraints are a bounded adaptation and are not a claim of unrestricted Python path equivalence.
- The owned Base executable fixture exercises actual SDK/host calls. An adjacent service-dispatch regression strengthens the stopped-clone/live-sibling assertions without claiming Python's scheduler has Go's process-running checks.

## Actual validation

The frozen v48 selected combined batch passed the same 75 root tests in ordinary and race modes. Both processes exited zero and were reaped; there were no failed or skipped roots and no input-hash drift. The App package elapsed times were 92.124 seconds ordinary and 329.253 seconds race. This is selected Base and adjacent coverage, not a full-project regression or complete business-plugin acceptance.

testdata/plugin-original-base-v48-evidence.json records the exact changed-code digests, both root-test lists, package results, log digests and the owned worker digest. cmd/owned-base-fixture contains the worker source; the compiled binary is not included. Set OWNED_BASE_FIXTURE_BINARY to the corresponding built worker for Base SDK tests. Adjacent roots also use the existing module, service and API fixtures described in the earlier checkpoint.

## Not included or accepted

- Later native/view integration layers and the market frontend are excluded from this frozen checkpoint.
- Common Form/Page/dashboard/navigation, complete native public-service bindings and continuous normal-server browser lifecycle acceptance remain unfinished.
- Final integrated full ordinary/race regressions, web checks/build, vet, Linux build, embedded assets and preservation of independent main changes remain required before module acceptance and main integration.

The separate earlier SDK 72/72 and artifact 23/23 results are historical evidence, not reruns performed for this Base checkpoint. No test or timeout weakening is claimed as a fix, and this checkpoint does not claim all Python business-input semantics are implemented.
