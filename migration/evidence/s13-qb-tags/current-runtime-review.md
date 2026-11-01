# Post-budget component gates — independent acceptance

**ACCEPT the current finite SDK/budget7, native3 and App2 roots in both normal and race modes.** These are fresh executions after the narrowly reviewed budget change, not reinterpretations of the pre-budget normal/race evidence. All historical reports remain intact.

## Actual terminal and input binding
The coordinator's copied tool receipts identify one serial execution stream: session14977, initial6a41d8, intermediate9b4c8c/3a04a0, terminal685238 exit0. Each receipt's individual result hash matches. Reviewer read raw test2json events, raw step exit files, results, all three declared input maps, generated-source copies and actual binaries; no tests were rerun.

| Gate | Roots PASS | Inputs | Program SHA256 |
|---|---:|---:|---|
| s13-qb-tags-budget-sdk01 | 7 | 1933 | 27235acd8f2207fc6dd8fcf26c1df9ef34aa52bed4d127cd73201efab7fb6b4b |
| s13-qb-tags-budget-sdk-race01 | 7 | 1970 | 44bc46272ad39639cc0379083c854c6601a9fea9fab7b85228bc510ea3948bba |
| s13-qb-tags-native02 | 3 | 2614 | 558d77ada92bf03eca4cac22606d25f6887a778b95d6facf6a58401704bf9de4 |
| s13-qb-tags-native-race02 | 3 | 2622 | 966e1305087069efcc803bf9afc39920e5cb9c094d1e698c1cc20988aca7d3a8 |
| s13-qb-tags-app02 | 2 | 6235 | e2a677c847d08bbd5d7e1519ecdbff981abeb4d136f82d292cd70d5b393c00ab |
| s13-qb-tags-app-race02 | 2 | 6244 | 5a49d86c4abfce647e72a0b710266aebc95f9d6db4b24db51b108252ab69856e |

Every gate has identical before/after-compile/after-test maps, zero current drift of its exact declared paths, matching program hash and matching preserved generated source. All commands exited0; no skipped/failed root or DATA RACE warning. Race go-list and compile both include -race. Resource checks are phase samples, not continuous monitoring. The runner inherits proxy/CA environment and only applies its declared build settings; no complete cross-process environment equality claim is made.

## Actual budget behavior
The four new SDK roots executed genuine default-Serve serial child processes using the same freshly compiled test executable (race-built children in the race run), not simulated session state. They verify that a selected900ms callback can outlast a400ms worker budget, same-object reuse and next-call reset, unrelated callback charging, cumulative worker work without replenishment, life cancellation with actual child Wait completion, invalid mode rejection before invocation, and nilHost denial without inventing authority. The unchanged all-host exclusion root also passed. Two existing qB tag wire roots passed with explicit null/False/empty/large-number preservation, strict class-only envelope rejection rules and no replay. This is finite instrumentation evidence, not proof of all possible schedules.

The native three-root suite re-executed on the new SDK dependency closure; aggregate dispatch/error provenance, unknown-effect protection, exact AttributeError restoration and False hash wire behavior retain their previously reviewed coverage. The App two-root suite re-executed after the changed session-selection file entered its closure; actual production-native one-info AttributeError and real>=3s delay passed (root elapsed3.02s normal/3.19s race), along with strict scope/configuration/Close/error boundaries. The App roots call the host resource directly: they do not independently demonstrate the full installed-worker App callSession selector.

## Closure judgment
The structural serial total-budget mismatch has a reviewed implementation and actual serial-SDK proof: only the trusted exact lookup callback pauses cumulative worker time; cancellation, other callbacks, startup/Stop, counts, bounds and authorization remain unchanged. A full ordinary installed-worker/server lookup lasting beyond30s is **still pending actual CLI evidence**, so no nine-round end-to-end timing claim is made here. The source's nine requested sleeps/whole-list selection/two-write cleanup remain the separate original recording oracle. No Python vendor, daemon, arbitrary host deadline, new TLS policy or additional S13 methods were executed or accepted by these gates.

Old leaf evidence may be inherited only under its separately verified dependency continuity; this report does not add a new leaf run. Server/worker builds and forthcoming CLI are separate layers. This six-gate seal is sufficient to continue that already planned ordinary-path acceptance without rerunning the same component matrix.
