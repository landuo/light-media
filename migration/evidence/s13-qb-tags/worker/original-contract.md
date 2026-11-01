# Owned qB tags worker — frozen preparation, not runtime evidence

Intended new path: `cmd/owned-qb-tags-fixture/main.go`. Base `55f2ed32b098ab67845cca24520202fa7562e9af`. This directory contains an audit-only candidate; no product, ledger, build, test, service or source execution by its author. The author is not its independent reviewer.

## Fixed ordinary entry

- Plugin ID `OwnedQBTags`, version `1.0`, entry `owned-qb-tags-fixture`.
- API key `qb-tags`, **POST `/qb-tags`**, bearer-authenticated ordinary host API. Worker has no network client/listener, file material, environment read, daemon credentials or arbitrary URL/capability selector.
- Only `downloader.qbittorrent.delete_tags` and `downloader.qbittorrent.get_id_by_tag` capabilities. Exact downloader ID `owned-qb-tags-qb`, kind `qbittorrent`; controller must configure only its owned synthetic peer.
- Standard **`plugins.Serve`**, not `ServeConcurrent`. Read-back `internal/plugins/worker.go` shows `Serve -> serve(...,false)` and explicit `ServeConcurrent -> serve(...,true)`; concurrent-session/transport-close advertisements are conditional on that flag. No fixture-side concurrency negotiation, timeout override, sleep, timer, retry or polling.
- Each listed business operation makes exactly one current typed SDK call. Native lookup owns its actual nine rounds and sleep. A slow lookup does not become nine callbacks.

## Strict finite inputs

Body at most 16 KiB; exact keys with duplicate/unknown rejection; 1–8 operations; optional non-null boolean `continue_on_error` / `probe_previous`. Decode every operation before invoking any HostCaller. These are owned fixture limits, not new product restrictions.

```json
{"operations":[{"kind":"qbittorrent","op":"delete_tags","downloader_id":"owned-qb-tags-qb","ids":["owned-a"],"tag":"owned-label"}]}
```

`delete_tags`: precisely kind/op/downloader_id/ids/tag. IDs only `["owned-a"]`; tag only `"owned-label"` or `"owned-fail"`. Failed-tag peer performs successful `removeTags`, then a fixed `deleteTags` HTTP 500; that is an already-selected uncertain outcome, not source False or a safe retry. `continue_on_error` can execute the next explicit distinct operation, never replay the failed one.

```json
{"operations":[{"kind":"qbittorrent","op":"get_id_by_tag","downloader_id":"owned-qb-tags-qb","tags":"owned-found","status":null}]}
```

`get_id_by_tag`: precisely kind/op/downloader_id/tags/status. Tags only `"owned-found"`, explicit `null`, or `"owned-empty"`; status must be present `null`. No hidden phase or status sentinel. The driver sets its own in-memory peer phase under a lock before a business call, not through a worker capability or new product setting:

1. Found phase: `[{"hash":"owned-found-hash","tags":"owned-found"}]`; source finds it after its first real 3-second sleep and performs actual owned remove/global-delete requests before returning the raw hash.
2. Attribute phase: `[7]` with **tags null**; falsey tags bypass filtering and first-row `.get` throws the actually observed direct AttributeError. Truthy tags would catch in the filter, so must not be substituted.
3. Slow-empty phase: each fixed `torrents/info` reply waits 500 ms then returns `[]`; worker submits tags `"owned-empty"`, status null exactly once. Nine native 3-second sleeps plus nine peer delays have a nominal 31.5-second sum, **not an asserted exact wall time**. Actual timing, count-nine proof and serial 30-second budget regression are separate coordinator gates, pending reviewed budget integration. This candidate does not bypass the existing budget.

## Output / identity / cold start

Snapshot structure retains prior ordinary fixture fields: pid, init_count, invocation, requested, completed, succeeded, failed, ok, interrupted, previous_probe and results. One object increments invocation; a fresh worker naturally resets it and init_count. No persistence or fake cold values.

Each result has index/kind/op/downloader_id/ok, raw `result` only on a typed SDK return, result_kind and host_calls. Delete uses `source_bool` with the actual bool and `source_truthy`. Lookup uses `source_json` and copied SDK RawMessage, preserving null/false/empty string; no truthiness guess. `ok/succeeded` means a completed typed SDK return, **not daemon mutation success**.

A direct SDK `*QBTagSourceError{Class:"AttributeError"}` yields `error_code="source_exception"`, `source_error="AttributeError"`, no result. The callback itself succeeded in carrying this envelope (`host_calls.succeeded=1`), while the SDK operation failed (`ok=false`, snapshot failed increments). Platform/unknown/cancellation errors remain bounded safe codes, no `Error()` text, cause, source traceback or credentials. Per-operation callback counts/JSON byte lengths are not daemon requests, physical frames or successful mutations.

Same worker is observed through actual PID/init_count/invocation and subsequent valid typed SDK calls after both selected failure and source exception. Cold proof requires a newly started host/worker and explicit normal call; fixture itself does not launch or shut down hosts.

## One explicit stale-caller observation

When `probe_previous=true`, attempt one prior HostCaller using fixed `delete_tags` frame `{op:"invoke",downloader_id:"owned-qb-tags-qb",ids:[],tag:"owned-stale"}`. Label: `previous_invocation_delete_tags_caller_denied`. This is **not read-only or a source no-op**: erroneously admitted global DeleteTags would be a mutation. It is only permitted against the isolated owned peer; the driver must reject/record unexpected `owned-stale` requests and require zero peer calls from this probe.

Denied requires an actual error, zero returned bytes, and exact `invalid_host_call` or `PLUGIN_CALL_CLOSED`. `available:false` is not proof. One failed callback is recorded independently of current business result counts. No captured successful request, task, upload or arbitrary old capability is replayed.

## Proposed finite business sequence (controller may split, not expand semantics)

1. First host: delete success + found lookup (two current callbacks; owned found phase).
2. Same worker: second-write selected failure + explicit successful delete (`continue_on_error=true`; two current callbacks, no automatic replay).
3. Same worker: null-tags AttributeError (one callback; successful wire envelope / failed SDK operation).
4. Same worker: slow-empty lookup plus explicit old-caller probe (one current callback plus one denied old callback); proves recovery after source error and, once separately authorized/run, serial long-callback budget handling.
5. Cold second host, same owned persistent configuration: found lookup (one current callback; fresh worker PID/invocation/init state, ordinary lookup and cleanup).

This is seven planned current callbacks and one old probe, not an execution receipt. Real source16/pure/native/SDK evidence are independently bound elsewhere; these small host scenarios do not replace them or prove a real qB daemon.

## Preparation checks

`prepare_candidate.py` reads only the exact hash-bound prior owned fixture and writes this audit directory; pinned gofmt formats the candidate. `manifest.expected.json` is a statically prepared expected projection, not an emitted executable manifest. Actual built `--manifest` must be compared before installation. No import of original app/vendor/native module, no Go list/build/test, no HTTP, no process probe, no DB or credential read occurred in this preparation.
