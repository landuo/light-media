# Owned S11 active-recognition worker — prepared, not executed

Base `911846b9a0cfe61da96d948d6b170f90f098c214`. Intended new command:
`cmd/owned-media-recognition-fixture/{main.go,main_test.go}`. This directory only
contains audit candidates. No installation, compilation, server/provider or
source execution is performed by its author.

* Manifest ID `OwnedMediaRecognition`, entry `owned-media-recognition-fixture`,
  version `1.0`; only capability `media.recognize`.
* API key **`media_recognize`**, POST path `/media-recognize`, bearer registration,
  JSON body. `--manifest` emits that same validated declaration.
* Uses **plugins.ServeConcurrent**, not the default serial Serve. The actual host
  must verify negotiated SupportsConcurrent before self-target discovery. This
  document and the unit root do not prove negotiation or runtime reentry.
* `get_state` returns true; `get_module` binds only
  `recognize_media -> recognize_owned`. Module receives the existing
  `{args:[],kwargs:{ten normalized source parameters}}`; it returns the ordinary
  field object/false/null directly, **not** the outer SDK `{value:...}` envelope.
* One current SDK call per selected operation (pre-canceled is zero callbacks).
  No worker retry, timeout extension, sleeps, native HTTP, credentials, config
  getter, persistence, arbitrary arguments, module names or endpoints.

## Finite public body and observations

```json
{"operations":[{"op":"module_hit","cache":false}],"continue_on_error":false}
```

One through eight operations; only `op` and optional `cache` (null/true/false).
Cache omission is retained and only the host applies source default true.
Unknown/duplicate keys or operations reject the request, not arbitrary RPC.

| op | fixed input / module behavior | actual boundary |
|---|---|---|
| module_hit | explicit TMDB 900043, movie, absent source; owned 66-field model | Plugin-first/full raw output, not native provider proof. |
| module_false | 900044; module returns false | Existing original falsey terminal can produce outer None without native. Actual App proof still required. |
| unsupported_type | 900043, `未知` instead of movie | Host's currently unimplemented type boundary; no fabricated None. |
| pre_canceled | 900043, movie; cancel only this operation before SDK | SDK admission observation, **not** in-flight host cancellation proof. |
| native | 900045, movie; module returns null | Real native fallthrough. **Prepared only; do not execute until coordinator/root authorizes the owned native HTTP seam.** Pending/error must remain failed, never be relabeled None. |

All calls have meta=share_meta=null. No registry or mutable metadata roundtrip.
The synthetic model starts from the existing 66-field `source-mediainfo.json`
default vocabulary, with explicit owned title, IDs and provider-shaped nested
payload. This is test data, not observed original output or a model oracle.
Original source/native model correctness belongs to their independent proofs.

Snapshot records PID/init_count/invocation, each typed SDK success/failure,
actual HostCaller counts/byte lengths, get_state/get_module/recognize counters,
and last64 fixed-input module observations. Successful callback envelopes and
successful typed results are separate. Error text is not echoed; only bounded
protocol codes or context categories are reported. HTTP200 alone is not success.

External snapshots take businessMu to keep their observation sequences usable;
reentrant discovery/module handlers never acquire it. stateMu protects counters
only and is never held across SDK/HostCaller. No saved old HostCaller or alias
registry. A cold new worker naturally starts init_count=1/invocation1 on first
business call; same-worker recovery uses explicit next operations/calls.

## Prepared unit gate / later actual gates

`TestOwnedMediaRecognitionFixtureManifestAndReentry`, package
`./cmd/owned-media-recognition-fixture`: validates manifest key/route, direct
handler reentry while an SDK call is active, error/pre-cancel recovery, complete
owned fields/large integer and negative input rejection. Its recording host is
explicitly not an App/native oracle. False/null direct module return assertions
do not claim successful native recognition. The actual public worker negotiation,
installed App/router/source dispatch and normal cold host still need coordinator
execution after independent review. No normal CLI driver is copied here.
