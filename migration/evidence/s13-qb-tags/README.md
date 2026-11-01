# S13: qB tag cleanup and delayed lookup

Finite backend slice on `55f2ed32b098ab67845cca24520202fa7562e9af`, for the fixed `plugins-refactor` delivery chain. Local acceptance is not remote delivery. The last confirmed remote remains `11f922896377cb0d02bf481ef0df10f11a1cb4c4`; Mac quota blocks the pending delivery chain. No main/NAS change or CI-green claim.

## Behavior and boundaries

This adds the original `delete_torrents_tag` and `get_torrent_id_by_tag`, including complete `get_torrents` filtering and the private first-ID helper, against core `8a83efeacd3e26b82be495e17d50146e31c96a60`. The prior four ordinary S13 controls retain their credit; the two remaining exact methods are TR `set_torrent_tag` and `change_torrent`. S13 is not complete, and reannounce is not silently added to the original eight-method scope.

Cleanup removes tags from selected torrents and then deletes their global tag definition; it does not delete torrent files. Nonthrowing source return values, including False/None, are ignored. Both version gates are fresh and unsupported nonraising endpoints remain no-op returns. A single native scope keeps selected-dispatch history across both writes: a later failure is not falsely reported as no effect, and selected writes are not replayed.

Lookup performs nine iterations, each beginning with a three-second wait. The complete client-side tag filter visits all rows and preserves source ordering, wanted-tag whitespace, returned-tag stripping, malformed-later-row failure and finally clearing the original list. Missing/null hash continues; False/empty string does not. Cleanup's ordinary source False does not erase the found value; platform authority/cancellation/uncertain-effect errors are not swallowed. The one observed escaping AttributeError has an exact class-only SDK envelope, never a message/trace/credential or wrapped boundary error.

Two fixed capabilities use one ordinary 128 KiB request each. Current enabled downloader selection, immutable configuration snapshots, host identity/generation/phase, guards, admission and Close remain enforced. Endpoint/credential selection is host-owned, not arbitrary worker RPC. Native support is the explicitly declared finite ordinary JSON domain; arbitrary Python objects/iterators or unverified vendor behavior are not certified. Existing independent qB TLS and cross-domain credential policy are unchanged.

## Narrow serial timing correction

The default `Serve` worker is serial. Its former 30-second whole-call budget could truncate the source's 27 seconds of waits plus I/O. A trusted per-invocation `ExcludeHostMethod` now pauses the existing cumulative worker timer only while the declared lookup callback runs. Other capabilities still consume the original budget; remaining worker time is not reset. Startup, Stop, wire/call-count bounds, capability authorization, lifecycle cancellation and the admitted serial HTTP-disconnect policy are unchanged. Concurrent workers retain their existing policy and reject this serial-only option.

This is not an arbitrary global timeout increase or a new hard total-host deadline. Source steps and existing native request/response/lifecycle limits remain finite; the documented loose I/O-count bound is not a hard wall-clock SLA. Actual serial child tests cover selected versus other callbacks, cumulative worker time, next-call reset, life cancellation/Wait, no new grants and rejection of incompatible options, plus one related old all-host-exclusion regression.

## Actual evidence

The original four complete source nodes were executed with recording ports: 16 cases, comprising 15 returns and one uncaught AttributeError. Recorded sleep requests alone are not 27 seconds of elapsed time. Pure leaf tests compare the independent source output, full ordered dependency/log/sleep traces and original list cleanup. Normal/race leaf evidence is reused only after unchanged-input verification. The budget-affected SDK/native/App closures were rebuilt and checked separately; earlier pre-budget results remain historical, not silently relabelled current.

The accepted ordinary installed-worker CLI uses two real normal Go hosts and fixed owned HTTP peers, not a real qB daemon. Hosts 24 and 79 exited naturally with code 0. Worker 41 performed invocations 1–6; worker 94 started fresh at invocation 1 after same-DB cold restart, without artifact refetch. Seven business requests made eight current SDK callbacks plus one explicit expired-caller denial. Callback success, typed SDK success and mutation certainty are separately reported.

The full nine-empty-read call actually took 31.543398536 seconds: nine real three-second native waits and nine fixed 500 ms owned response delays, exactly nine info requests and no cleanup. A found lookup and the direct AttributeError path each waited over three seconds. One second-write HTTP 500 remained an unknown effect without replay, followed by a distinct successful operation on the same worker. The old caller produced an actual denial and zero peer work. All 37 peer requests and non-login response bodies are retained; these are protocol observations, not physical plugin-frame counts or real media state.

The first CLI attempt really failed installation: the self-built fixture used an invalid hyphenated API key. Its original failure, input binding and pre-execution review gap are retained. Only the fixture key/guard and a focused manifest/route test were corrected; the URL path stayed unchanged, and the already-built product server was reused. The second attempt is a new execution, not a rewritten first result.

No browser/UI, real downloader, media/NAS, vendor-runtime or complete-parent equivalence is claimed. Private databases, host logs, binaries, dependencies and credentials are excluded from publication; exact full raw build/test evidence and recovery checkpoints are retained externally. The structural ledger's inherited unresolved errors remain explicit, not a globally green final gate.
