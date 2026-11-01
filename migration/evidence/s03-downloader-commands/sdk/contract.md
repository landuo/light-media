# S03 SDK private protocol — historical legacy contract and material/v1 successor

2026-10-31. The coordinator accepted this proposed protocol before implementation. Only the two new SDK files are owned here. App/native/leaf, global transport limits, environment and ledger are unchanged by this owner. This contract is not executed native/daemon proof.

## Current negotiated bytes successor

The public eight command methods and result types below are unchanged. For a typed `[]byte` argument, an actually negotiated material/v1 peer uses `upload_material_begin` (ordinary callback, args exclude content, total+SHA) → exact ToHost/bytes grant → bounded private write pages → finish → one ordinary `upload_material_submit`. The current page maximum is 256 KiB. Successful Add therefore uses two ordinary callbacks; private material frames are not legacy append callbacks. Length/SHA/current identity/capability/generation are independently checked by the host, and claim consumes a terminal one-use handle before native dispatch.

Only this negotiated bytes path removes the old extra 16 MiB material cap. Ordinary request/frame/callback/time restrictions remain; material traffic is separately accounted, not smuggled through ordinary counters. The public `[]byte` API, one necessary caller-input snapshot and final native materialization remain O(n). The native selected Add streams multipart/base64 from owned bytes rather than retaining multiple whole encoded bodies.

No negotiated failure falls back or retries using legacy upload/invoke. Before submit, only best-effort scope-owned abort is permitted; after any submit attempt there is no abort/replay and uncertain selected effects remain errors. Scope cleanup removes only its newly created spool, not source backup/cache/hash files. An unnegotiated peer continues to use the bounded legacy protocol below. Typed nil bytes still mean empty bytes, not JSON null. See [current material documentation](../../../../docs/plugin-downloader-material.md).

## Original 2026-10-31 contract (legacy path)

## Public types and eight capabilities

`DownloaderCommandArgs map[string]any` preserves omitted keys versus explicit nil. `SharedQBittorrentCommands` and `SharedTransmissionCommands` each bind `Host HostCaller` and an explicit `DownloaderID string`.

- `AddTorrent(ctx,args)` returns `QBittorrentAddResult{Success bool, IDs []string}` or `TransmissionAddResult{Value json.RawMessage}`, plus Go error.
- `StartTorrents(ctx,ids)`, `RecheckTorrents(ctx,ids)` and `DeleteTorrentsKeepFiles(ctx,ids)` return `(bool,error)`.
- Capabilities are exactly `downloader.{qbittorrent,transmission}.{add,start,recheck,delete_keep_files}`. No generic RPC or delete-data boolean exists.

qB's `SourceTruthy()` remains true for its two-member source tuple even when Success is false. TR Value is finite observed native response JSON only, pending the native mapping contract; it does not invent a complete Python Torrent, Fields or its properties. `null` is a nonempty RawMessage, not an absent value. Host errors and uncertain effects remain Go errors, never fabricated source false/null.

## Legacy private frames (when material/v1 is not negotiated)

Nonbytes add: `{op:"invoke",downloader_id,args}` with required content, including explicit null/string. Controls: `{op:"invoke",downloader_id,ids}` with required ids, including null/empty values without normalization.

Byte add is one logical call, with no submit retry:

1. `{op:"upload_begin",downloader_id,args:<without content>,total,sha256}` -> exact `{handle}`.
2. One or more `{op:"upload_append",handle,offset,data:<base64>}` -> exact `{handle,offset:<cumulative next>}`. Pages are 1 MiB raw except the last; at most 16 pages.
3. `{op:"upload_submit",handle}` -> final source-value reply.
4. Optional best-effort `{op:"upload_abort",handle}` -> exact `{aborted:true}` is allowed only before a submit attempt. It is memory reclamation, never rollback or proof of no external effect. Same capability/HostCaller/authority; no fallback or retry.

A typed nil `[]byte` is empty bytes: begin(total 0, SHA of empty) then submit, no append. An untyped nil content takes invoke with JSON null. Handles are `u_` plus 64 lowercase hex digits, invocation/capability-owned, not paths or credentials. Begin freezes host-selected enabled configuration and typed arguments; SDK never supplies endpoints/authentication. Host checks every frame and atomically consumes before dispatch; SDK must not resubmit on any error or malformed reply.

Final replies are exact `{success:bool,ids:[string...]}` for qB add, `{value:<ordinary JSON>}` for TR add and `{value:bool}` for controls. Required/null/unknown/duplicate/case-alias/trailing-data ambiguity is rejected. TR's opaque JSON keeps number lexemes/code points rather than a float/bool or fake object projection.

## Bounds and proof level

For the legacy path only, generic requests remain 128 KiB, append gets `4*((1MiB+2)/3)+512`, and material maximum is the inherited 16 MiB. The historical ordinary frame/wire/callback budgets are unchanged. Source Python has no identical material bound; the negotiated successor above addresses that extra material limit without rewriting this old protocol.

Ordinary UTF-8 Go values and explicit raw JSON are supported; custom marshalers/arbitrary objects and implicit non-content []byte coercion are not. Args are snapshotted before begin; byte content is snapshotted before hashing/upload. There is no retry after invoke or submit. A malformed begin without an unambiguous admitted handle is left to scope-close reclamation rather than guessing a token.

Tests will use only a recording HostCaller, owned bytes/values and target boundary failures. They do not grant or prove filesystem/downloader operations, real upload side effects, complete Torrent fields, native selectors or ordinary host permission enforcement. Go execution is coordinator-only.
