# S13 final TR native — bounded frozen candidate

Base `cd21eaf2bd8aeeb4af012e7a934faad8c5b9b288`, source `8a83efeacd3e26b82be495e17d50146e31c96a60`, pinned transmission-rpc 7.0.11 client text SHA `5d34047c37bcadb00f3320fd8dbbe46aae6583acb5fd7877ed9a4ca0eea4bf34`. External candidate only; no Go, source/vendor import, network or daemon execution by this author.

## Fixed interface and request

Implement only optional `TRChangePort.Change(context.Context, TRChangeCall)(any,error)` on existing `sharedTRCommands`. Its prepared DTO has `IDs`, `Labels *[]string`, four source limit pairs, and nonserialized trusted `Log LogFunc`. No generic args map in public/leaf API, endpoint selector, kwargs, material or file operation. Old TRC and QBC interfaces unchanged.

One existing owner.run/native lock/private monotonic proof covers all work. Source-empty args returns original `ValueError("No arguments to set")` before require_ids and before selected/readonly request. Nonempty args retain false/0, exact camelCase and existing require_ids parsing. Existing readonly session handshake, JSON legacy envelope and one selected `torrent-set` remain; no selected replay on 409/500/transport error. Result content is ignored only after the actual transport says success.

`SeedRatioLimit` int64 remains an integer JSON token; finite float64 stays a float token including `0.0`, `1.0`, and signed zero, rather than Go's default integral-float collapse. The leaf supplies exact conversion/rounding; native does not repeat it.

## Warning / minimal old hook

Pinned client `change_torrent:699–700` calls `_rpc_version_warning(16)` for non-None labels, then still sends. `_rpc_version_warning:365–374` emits original WARNING text: `Using feature not supported by server. RPC version for server %d, feature introduced in %d.` It is not a low-version rejection.

Only existing-file delta is one cached session response field and saving `trInit`'s already-received map. No additional request, old method validation, old signature or transport change. New labels path reads actual cached `rpc-version`; missing/malformed/out-of-int64 is an explicit unproved native response boundary, never invented version17. Actual source normal constructor obtains the session version before method use; the existing host native factory is lazy and this candidate preserves that established lifecycle.

Leaf `t.Log` flows in DTO.Log to the same App `logOriginalPluginContext(...,"downloader_command",...)` redacted context sink. Low-version fixed warning has no credentials, URL, input labels or worker-selected event. A missing trusted sink on this new low-version path is an explicit host-wiring Boundary, not silent warning loss. App normally provides it. No global logger or logger mutation API.

## Error provenance / retained boundaries

Known local no-args/require_ids errors alone can become direct SourceException for the original leaf catch. After owner.run, recovery requires this call's same direct pointer, exact private NotDispatched seal/native class/same cause, no selected delegation, native terminal, live ctx and successful final guard (unchanged cause). No generic errors.As unwrapping. Owner/cancel/Boundary/unknown or selected error never becomes source False. The all-None source recording-port True case is not native-vendor success: actual composed leaf/native must be False plus source ERROR log.

Transmission strict TLS, trust_env=False, connect+idle timeouts, current guard, ordinary request/response limits and Close stay unchanged. qB's scoped certificate exception does not apply.

## Finite planned owned gates (not executed)

1. `TestSharedTransmissionFinalWireAndWarning`: real owned HTTP legacy session409/200, RPC15 warning while selecting, actual full camelCase/false/0/float0.0/1.0/int0/empty labels, same-client cached session, old Stop adjacency.
2. `TestSharedTransmissionFinalValidationAndUnknown`: local noargs before ids/no HTTP, invalid ids, authority/pre-cancel, selected500 exactly once and no false downgrade; final owner guard overrides local source validation.
3. `TestSharedTransmissionFinalActualLeaf`: actual new leaf and native with fixed HTTP, unique string-label membership, source string"0"/negative/2.675/zero limits, actual noargs false/error-log composition. No exact Python hash-seed label order claim.

3 roots require new leaf DTO/API integration first; smallest package command is coordinator-owned `go test -mod=readonly -buildvcs=false ./internal/plugins/iyuuautoseed/native -run '^(TestSharedTransmissionFinalWireAndWarning|TestSharedTransmissionFinalValidationAndUnknown|TestSharedTransmissionFinalActualLeaf)$' -count=1`. No binary has been built here; no native/full-S13/runtime acceptance claimed.
