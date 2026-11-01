# S11 fixed owned-provider App launcher — prepared, NOT executed

One root `TestOriginalMediaRecognitionOwnedWorkerProviderAndCold`, intended
`internal/app/plugin_shared_media_recognition_owned_worker_test.go`.
Author prepares only; no Go build/test, worker/server, listener, source or network
has been executed by this preparation. Independent review and the coordinator's
serialized compile/runtime gate remain required.

## Actual layer the candidate will exercise

`App.New` + owned settings persistence + ordinary auth setup + actual artifact
index/install API + installed external Go worker -> its current public SDK ->
new active-recognition host handler -> actual reentrant self module (returns
None) -> new native reader/cache/full-model path -> fixed owned HTTP provider.
The test checks actual negotiated SupportsConcurrent, not just a manifest flag.
Warm/cold uses real `App.Close`, same private directory `New`, actual
`initializeOriginalPlugins`, auth login and same installation generation.
No reinstallation or artifact re-fetch is permitted. It does not call App.Start
or run unrelated background schedulers. Its controller calls use App.HTTPHandler
through the existing httptest recorder, NOT a socket/controller listener and
NOT the ordinary cmd/server process. Provider and artifact peers are actual
owned HTTP listeners during a future authorized test execution.

The ordinary CLI driver is a separate proof: it calls only plugin-first owned
module operations and NEVER the native operation or a configured provider key.
Neither layer proves real TMDB service operation, all source domains, UI, metadata
name recognition, mutable metadata roundtrip or S11-wide completion.

## Trusted, fixed test-only transport

`catalog.NewWithTransport` is the existing constructor seam. The injected client
is installed with its matching immutable credential and resolved current proxy
snapshot under catMu; this prevents a.catalog/native client selection silently
replacing it. Resolving current proxy config uses os.Getenv read-only and failure
is terminal; no proxy, CA, hosts, settings-proxy or process environment rewrite.
The synthetic catalog key is deliberately owned, not extracted from anywhere.

Every logical request must be GET to official HTTPS api.themoviedb.org with
exact `/3/movie/900045` or `/3/tv/900045`, original fixed append fields, zh-CN and
the owned key. No body, credentials in headers, extra/duplicate query keys,
other IDs/path, CONNECT or arbitrary URL is accepted. Only after validation does
the test construct a NEW request to the one captured httptest endpoint, exact
`/owned/movie` or `/owned/tv`, with no original query/key/cookie/auth headers.
No official URL reaches a network transport, no fallback and no redirect follow.
CloneForSourceRecognition creates another transport for that SAME endpoint;
this root does not inject/retry transport failures or claim retry equivalence.
This is explicit approved test routing, not unchanged production networking.

## Finite steps and observable assertions

10 business calls / 10 current SDK host callbacks, 9 provider HTTP reads:
1. movie default: movie-a; 1 read.
2. changed peer phase, default: cached movie-a; 0 read.
3. cache=false: movie-b; 1 read, replaces snapshot.
4. cache=null: movie-c; 1 read, source falsey/fresh preserved.
5. cache=false: actual owned404 valid errorJSON -> None; 1 read, error snapshot cached.
6. peer now recovered, default: cached None; 0 read.
7. cache=false: recovered full movie; 1 read, same worker.
8. native_tv/cache=false: TV full model/seasons; 1 TV read.
9. native_unknown/cache=false: TV success THEN movie errorJSON; exactly ordered
   2 reads despite the first success; returns full TV model.
10. cold new App/worker same DB, default: new movie response; 1 read.

Each warm step checks exact cumulative logical AND actual peer traces, PID/init/
invocation continuity, one successful callback, real module-decline trace, source
mtype/cache normalization and model/None. Logs retain per-step trace, callback
count and result SHA. A denied request that never reaches the owned peer cannot
satisfy an expected source error/None step. No retry/switch route after a denial.
Only the two expected artifact GETs are allowed across both App instances.

Success models contain the complete existing MediaInfo default vocabulary,
provider-specific nested extension with exact integer900719925474099312345,
source ID/type/title, TV seasons, and IMDb `owned-not-imdb` (not a UI ID gate).
`recognize_cache_hit` remains false even on HTTP snapshot hits: no metadata cache
is claimed. Payloads are owned protocol data, NOT original source oracle outputs.
Original selector/cache/provider differential evidence is independently bound in
native candidate04 and must not be credited as this launcher execution.

## Dependencies / execution handoff

Requires worker candidate02 (adds only native_tv/native_unknown), SDK candidate02,
App active-handler candidate04, native candidate04 and their actual integration.
Runner must bind the rebuilt `OWNED_MEDIA_RECOGNITION_WORKER` executable and full
actual App compile closure, including helper sources and embedded defaults.
The named env is only a prebuilt-artifact path; this test does not build or set it.
No binary hash is invented in the prepared manifest. Original worker candidate01
and ordinary CLI preparation remain immutable historical inputs; use the explicit
worker02 rebind supplement for the final CLI program binding.
