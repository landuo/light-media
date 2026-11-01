# TR final two ordinary methods — candidate API

Base `cd21eaf2bd8aeeb4af012e7a934faad8c5b9b288`; fixed source `8a83efeacd3e26b82be495e17d50146e31c96a60`, complete TR methods 168–181 and 388–423. Actual source23 raw `1c3a9652c19898ed1aa98d63b1650f07587dee87654384490047e7872f62671e`: nineteen returns, four escaping ValueErrors. Original label list order stays in the raw oracle; comparison normalizes **string-label membership and cardinality only**, not other arrays or trace order. No product edits or runtime acceptance from this preparation.

## Two ordinary SDK calls, new file only

Intended `internal/plugins/shared_transmission_final.go` / `_test.go`:

```go
const TransmissionSetTagsCapability = "downloader.transmission.set_tags"
const TransmissionChangeCapability = "downloader.transmission.change"
func (t SharedTransmissionCommands) SetTorrentTag(ctx context.Context, ids, tags, orgTags any) (bool, error)
func (t SharedTransmissionCommands) ChangeTorrent(ctx context.Context, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit any) (bool, error)
type TRChangeSourceError struct { Class string }
```

Each method issues exactly one inherited ordinary 128 KiB HostCaller request, no retry/upload/poll:

- set_tags: `{op:"invoke",downloader_id,ids,tags,org_tags}`.
- change: `{op:"invoke",downloader_id,hash_string,upload_limit,download_limit,ratio_limit,seeding_time_limit}`.

Every listed field is required in the wire shape; present null preserves source None/default. SDK accepts ordinary finite JSON through existing validation, not caller endpoints/credentials or arbitrary RPC/kwargs. Exact response `{value:bool}`. Only **change** additionally accepts exact `{kind:"source_error",source_error:"ValueError"}` for direct source conversion failures outside try. No message/traceback, other class, mixed result/error, unknown field or duplicate key. Platform/guard/cancel/unknown errors stay HostCaller errors. App may export only a direct `*downloadercommands.SourceException` with Class ValueError from preparation, after its current guards; never errors.As or a wrapped/delegated exception. Tags returns only bool; its source concat/set/call errors are inside catch.

## Pure leaf optional fixed port, old interfaces unchanged

Intended `internal/plugins/downloadercommands/tr_final.go` / `_test.go` plus full raw oracle `testdata/source-tr-final.json`:

```go
type TRChangeCall struct {
    IDs any
    Log LogFunc // fixed native low-version WARNING sink only; never a serialized argument
    Labels *[]string
    UploadLimited *bool
    UploadLimit *int64
    DownloadLimited *bool
    DownloadLimit *int64
    SeedRatioMode *int64
    SeedRatioLimit any // nil omitted; int64 zero or finite float64, preserving source numeric type
    SeedIdleMode *int64
    SeedIdleLimit *int64
}
type TRChangePort interface {
    Change(context.Context, TRChangeCall) (any, error)
}
func (t Transmission) SetTorrentTag(ctx context.Context, ids, tags, orgTags any) (bool, error)
func (t Transmission) ChangeTorrent(ctx context.Context, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit any) (bool, error)
```

Existing `TRC`, `QBC`, eight old calls, four-control file and qB tag pair remain unchanged. Non-nil old client lacking the optional port is BoundaryError, not an absent-client source branch. Native one Change call enters one existing owner.run; no nested owner.run/lock/proof reset. This DTO allows exactly labels or the four fixed source preparation pairs; no map of arbitrary keywords.

set_torrent_tag keeps source early missing-client/falsey ids/falsey tags returns; merges supplied `(org_tags or []) + tags`, never prereads labels. Successful string-label output is a set (unique membership/cardinality), no sorted-wire/Python hash-seed-order claim. Captured string-concat and list/dict-unhashable errors are source TypeError inside catch. Unproved mixed hashable-label semantics remain explicit BoundaryError, not guessed coercion. Ordinary nonthrowing dependency results are ignored; direct source exceptions log and return false, wrapped/authority/unknown failures do not.

change_torrent returns false for falsey hash before conversion and has **no early missing-client return**. Convert non-None values in original upload/download/ratio/idle order **before** call catch. Original truth is evaluated before conversion (nonempty string "0" stays truthy). Nil fields are omitted, explicit zero kept; output fields stay original camelCase at native boundary. Int conversion is bounded signed int64; ratio uses finite binary64 and correctly rounded two-decimal formatting of the exact binary64 followed by parsing, **not multiply-by-100 rounding**. JSON floating number tokens follow binary64 decoding before truth/int conversion; overflow/unproved type domains are BoundaryError, not wrap or fabricated source errors. Reuse existing ordinary JSON, truth and Python repr components where exact; no new general parser framework.

Missing client after valid change preparation produces original caught NoneType AttributeError/log/false. Actual malformed numeric strings produce direct ValueError without call or caught-call log, as all four source observations show. Original all-None call reaches the port: recording nonthrowing reply proves only wrapper behavior; production native must supply pinned vendor `ValueError("No arguments to set")` before any selected HTTP, which the wrapper catches to false. Do not promote the recording all-None True case to native success.

Native keeps original require_ids, label-version warning (not a hard skip), camelCase kwargs and exact transport guards/provenance. Leaf passes its existing Log sink via DTO.Log for that fixed warning only; no setter/global logger/new remotely supplied callback. Selected/unknown effects cannot be downgraded to direct SourceException. Native/host/warning/ordinary-worker/CLI gates and independent source review are separate from this SDK/leaf candidate.
