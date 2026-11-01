# S11 App04 + owned launcher03 — actual normal/race acceptance

**ACCEPT for the exact six-root App component/installed-worker launcher gate.** No new execution was performed by this reviewer.

| Actual gate | Inputs | Roots | Installed worker PID warm → cold |
|---|---:|---:|---:|
| normal02, 01:52:28–01:52:59 UTC | 6,266 | 6 PASS, 80 retained model subcases PASS | 131 → 140 |
| race02, 01:53:24–01:54:13 UTC | 6,275 | 6 PASS, same 80 subcases PASS | 161 → 171 |

Raw test2json events, command exits, three input maps, generated-source copies and both programs per gate were independently read and rehashed. Each gate's before/after-compile/after-test maps are equal and current inputs had zero drift. Normal/race repository source inputs match; their whole compiler/dependency maps are correctly distinct. Both App **and installed worker** were built with `-race` in race02, matching `go list -race`. No failed/skipped events or DATA RACE warning appear in its persisted output. Parent-reported outer tool terminals are recorded with their attribution, not invented session-store receipts.

## Actual owned chain

The launcher used real App.New, settings persistence, auth/install HTTPHandler, prebuilt artifact, resource-contract SDK callback, negotiated concurrent self-module reentry, native cache/provider/model path, App.Close, same-directory reopen and initializeOriginalPlugins. It did **not** call App.Start or execute ordinary cmd/server. Controller requests are in-process Handler calls; provider/artifact HTTP listeners are real controlled local peers.

In each execution the nine warm calls held one PID/init count with invocations 1–9; the cold call used a distinct PID and invocation/init 1, unchanged installation generation/version and no reinstall. Exact cumulative logical **and actual peer** traces were asserted after every step. The resulting sequence was:

`movie:movie-a, movie:movie-b, movie:movie-c, movie:error, movie:recovered, tv:tv, tv:unknown, movie:unknown, movie:cold`.

This is 10 SDK callbacks, 9 provider requests, and the separately asserted single index + single worker artifact GET. Default cache hit retained the identical first model SHA without another provider request. False and null forced fresh reads; valid owned 404 error JSON produced None, was cached for the next default call (same null SHA), then fresh same-worker recovery returned a model. Explicit TV returned seasons. Null type read TV **then** movie even after TV succeeded; a valid movie error left the TV model. Cold created a new HTTP snapshot state and fetched its new movie. All warm result hashes and trace fields match between normal and race, except PID. Ten distinct actual module-request call IDs exist in each raw log.

These are assertions on actual returned SDK model data, not a recording replacement HostCaller. Selected identity/title fields, embedded-default key presence, raw nested integer900719925474099312345 and TV episode projection were exercised. Only result hashes and assertions—not separate complete response-body files—are persisted by this gate. The synthetic provider values are **not original source oracle results**, and no real TMDB origin, TLS/proxy path, metadata-name cache, TTL expiry or arbitrary-input equivalence is inferred.

## App04/source correspondence

Current handler SHA `3e277651…` matches the reviewed App04. It preserves source kwargs precedence/default cache=true, explicit null/false and actual interface-nil meta; no replacement endpoint is worker-supplied. Plugin dispatch runs before native; only null/empty continuation reaches it, false is not silently treated as null, and list handling remains separately bounded. Fresh is inherited OR. Current identity/config and parent guards surround discovery/native/result; self discovery checks **actual negotiated concurrency** before nested calls, resource capability is not granted to the old base contract. Only a direct native-defined source exception is report-and-None after current guard; wrapped/platform/canceled/unknown failures are not blanket-converted.

The two normalization/admission roots test pure wire/scope components, not installed workers. The snapshot root tests real Go cache ownership/fresh/failure/cancel/expiry with a controlled clock, not original cachetools execution. The model/group root tests finite augmentation/category/error seams. The retained source-model root independently consumes its bound 80-case oracle at runtime. Do not flatten these distinct layers into six real-provider tests or use this gate to replace the separate native/source reviews.

## History and remaining boundaries

App01's actual compile failure (two named-type errors, zero tests) remains bound and unchanged. Candidate03 only fixes the two test conversions; the earlier static-review miss is retained explicitly. The original 31/34-entry preparation maps are not passed off as compiled closures. Normal/race now bind actual programs and full maps. Program digests are in the attached machine readbacks.

No concrete blocker was found within this executed finite scope. Ordinary server CLI, production-origin HTTP and overall S11 completion remain separate. The normal CLI driver must bind its own final server/worker inputs; it is not enlarged to native calls by this test launcher.
