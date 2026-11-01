# S11 explicit-ID SDK — narrow wire contract (prepared, not executed)

Base `911846b9a0cfe61da96d948d6b170f90f098c214`; source MoviePilot
`8a83efeacd3e26b82be495e17d50146e31c96a60`,
`app/chain/__init__.py:637–747` (`ChainBase.recognize_media`).
The original eleven named arguments, not another chain/UI entry, are the API.

```go
const MediaRecognizeCapability = "media.recognize"
type SharedMediaRecognition struct { Host HostCaller }
type RecognizeMediaRequest struct {
    Meta, MType, Source, MediaID, TMDBID, DoubanID, BangumiID, AniListID,
    EpisodeGroup, Cache, ShareMeta json.RawMessage
}
func (s SharedMediaRecognition) RecognizeMedia(
    ctx context.Context, input RecognizeMediaRequest,
) (*ModuleMediaInfo, error)
```

## Fixed request and result

One ordinary HostCaller call, capability above, request exactly `{"args":{...}}`.
The only argument keys are `meta`, `mtype`, `source`, `mediaid`, `tmdbid`,
`doubanid`, `bangumiid`, `anilistid`, `episode_group`, `cache`, `share_meta`.
Nil RawMessage means **absent**; non-nil `null` means explicit None; non-nil empty
bytes are invalid, not silently omitted. SDK never fills defaults or trims /
normalizes values. Host applies source defaults (cache=True, all others=None)
only to absent fields. Explicit cache=false and cache=null remain distinct.
Raw numeric lexemes (including large integers and exponent forms) remain raw;
there is no float64 intermediate. Original MediaType values are `电影`, `电视剧`,
`系列`, `未知`; any enum conversion / supported first-slice check belongs to host,
not an invented SDK movie/tv alias normalization.

First vertical slice requires meta/share_meta absent or JSON null. Non-null
values are rejected before HostCaller; no object registry, parser, title lookup,
or guessed MetaObject state. Other nine arguments are ordinary JSON values,
preserved rather than coercing the Python annotations into runtime restrictions;
host explicitly admits only its implemented explicit-TMDB-ID branch. Merely
transporting a value does not prove that every such value is implemented there.
No arbitrary argument names, endpoint, credential, provider RPC or config field.

Successful response exactly `{"value":null}` or `{"value":{...}}`.
None returns a nil pointer; a JSON object returns ModuleMediaInfo with **all** its
raw fields preserved, not a compact catalog row or a fabricated default model.
The SDK does not infer full-model correctness from field presence; host/source
projection and actual component tests must prove that separately. Outer unknown,
missing, duplicate fields and nested duplicate JSON keys are invalid. MediaInfo
fields are intentionally open because the existing complete raw model is shared
with ordinary module callbacks. Returned byte slices are owned by the result,
not aliases of HostCaller output. Keys materialized in the Go field map must be
lossless Unicode; a lone surrogate in a field name is an explicit boundary, not
silently replaced or collapsed. Raw field values retain escaped code points.

## Existing bounds and error/lifetime boundary

Request <=128 KiB, response <=existing MaxPayloadBytes (16 MiB). No global ABI,
frame, hostcall count, timeout or worker budget change. Nil context/host or
pre-canceled context performs zero callbacks. Exactly one callback otherwise;
no retry, cleanup callback, retained handle or endpoint. Host errors are returned
unchanged; cancellation observed after a successful callback is returned rather
than exposing a result. No blanket conversion of errors into None, and no new
unproven source-exception class envelope in this wire slice.

Reuse existing strict ordinary JSON validation (including duplicate-key and raw
numeric handling); no changes to older shared SDK files/helpers. Prepared paths:
`internal/plugins/shared_media_recognition.go` and `_test.go`, stored first as
audit `.go.txt` candidates only. Recording-HostCaller tests prove wire/boundary
only; inherited source10 is not new native/cache/worker evidence or a new oracle.

## Forward state and host obligations

Do **not** later admit non-null meta by simply removing this validation: source
mutates meta.type/meta.tmdbid, so a separately reviewed explicit state roundtrip
is required before that extension. This nil-meta return shape does not silently
claim mutation propagation. Host must implement fresh(not cache), source ID
priority, original plugin/core order, safe actual self-reentry/retained scope,
native full MediaInfo or None and no-meta share early return. Sending cache as a
label is not fresh-context implementation. These host/native tasks and their
actual oracle observations are owned separately; this SDK does not fake them.
