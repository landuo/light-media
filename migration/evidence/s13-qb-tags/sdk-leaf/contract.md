# qB tag cleanup and delayed lookup — frozen internal API

Base `55f2ed32b098ab67845cca24520202fa7562e9af`; candidates only, no product write/build/run. Source four complete nodes at fixed `8a83efe…`: get_torrents224–252, delete_torrents_tag304–319, private last-ID367–381, get_torrent_id_by_tag383–399. Actual16 raw `267d2e4fa62ed38ce53e4e6a6bf8ebfc5676058b413adbc072e74aa81c9c8617`, independently accepted `d997cf68…`. Fifteen returns and one uncaught exception, not sixteen successful mutations.

## SDK, two new capabilities only

New `internal/plugins/shared_qbittorrent_tags.go` / `_test.go`, with methods on existing `SharedQBittorrentCommands`:

```go
const QBittorrentDeleteTagsCapability = "downloader.qbittorrent.delete_tags"
const QBittorrentGetIDByTagCapability = "downloader.qbittorrent.get_id_by_tag"
func (q SharedQBittorrentCommands) DeleteTorrentsTag(ctx context.Context, ids, tag any) (bool, error)
func (q SharedQBittorrentCommands) GetTorrentIDByTag(ctx context.Context, tags, status any) (json.RawMessage, error)
type QBTagSourceError struct { Class string }
```

Exactly one normal128KiB request each; no upload or retry. Delete request `{op:"invoke",downloader_id,ids,tag}`; lookup `{op:"invoke",downloader_id,tags,status}`. All listed fields required, present null preserved. Status null represents the original optional default, with no empty/string normalization. No 40hex-only selectors, sort, deduplication or bool/empty-value coercion.

Delete exact response `{value:bool}`. Lookup exact `{value:<ordinary JSON>}` returns copied RawMessage: `null`, `false`, `""` remain distinct. Lookup alone also admits exact `{kind:"source_error",source_error:"AttributeError"}` for the independently observed escaping original exception. Class only, never text/traceback/credentials; no other class or mixed value/error envelope. This mirrors the **pattern** in existing `shared_torrent_material.go:DownloadTorrentSourceError`/prepare source_error decoding, but does not modify/reuse its wider class whitelist or old decoder. All platform/authorization/cancellation/uncertain-effect errors remain HostCaller errors, never source envelopes. App may export only an unwrapped/classified escaping source exception after its current scope/config guards; a wrapped boundary containing SourceException is not eligible.

## Pure leaf / optional scope port

New `internal/plugins/downloadercommands/qb_tags.go` / `_test.go`; no edits to old `QBC`, `TRC`, `QBittorrent`, source four controls or old eight methods.

```go
type QBTagRows struct { Values []any }
type QBTagPrimitives interface {
    Info(context.Context, any) (*QBTagRows, error) // status; fixed torrent_hashes=nil
    RemoveTags(context.Context, any, any) (any, error) // ids, tag
    DeleteTags(context.Context, any) (any, error) // tag, global definition deletion
    Sleep(context.Context, time.Duration) error
}
type QBTagScopePort interface {
    WithTagScope(context.Context, func(context.Context, QBTagPrimitives) (any, error)) (any, error)
}
type QBTagCommands struct {
    QBittorrent
    Sleep func(context.Context, time.Duration) error // nil-client clock ONLY
}
func (q QBTagCommands) DeleteTorrentsTag(ctx context.Context, ids, tag any) (bool, error)
func (q QBTagCommands) GetTorrentIDByTag(ctx context.Context, tags, status any) (any, error)
```

Non-nil existing Client must implement the optional scope port or return BoundaryError. **One scope per complete public call**, one existing native mutex and one monotonic mutation proof; primitive methods never call owner.run or acquire a nested lock. Scope callback is only an internal typed Go closure, not a worker-supplied remote RPC. Native guard must run on every actual request and before/after sleep; same proof survives Info/auth/version/RemoveTags/DeleteTags. Any selected-dispatch/guard failure must be classified into Boundary/unknown **before** returning to leaf source catch, not merely by final outer run defer, or the source catch would hide partial effects. Final App guards still apply.

Nonnull client sleeps through scoped primitive Sleep, production using existing native cancellable sleep. Nil client still performs original nine sleep requests of3s; the wrapper's Sleep exists only for this no-client path, nil defaults to a real cancellable timer. No synthetic early return or ten-round claim.

Info returns a fresh mutable list container: ordinary JSON row values in source order. A nil container is a native boundary, not fabricated source None. Truthy tags cause complete filtering before selecting first result and **finally clear the original Info list**, including later-malformed-row failure. Falsey tags do not enter that finally; no-filter list remains unchanged. `QBTagRows.Values` enables actual source-like clear observability for recording/native ownership without claiming Go slice aliases are Python lists. No target-generated oracle: tests compare source result/type/truth, full ordered calls/errors/logs/sleeps, and `dependency_return_values_after_source`.

Missing/null hash means continue; any present non-null value including false and empty string means cleanup then stop. Source cleanup False is ignored by lookup; platform errors are not. First-row `.get` remains outside private-helper catch, so ordinary nonmapping row can escape as classified SourceException. Only finite ordinary JSON/list/row semantics are supported; arbitrary Python objects/iterators remain explicit boundaries. Original type errors inside get_torrents are caught/logged there; outer errors are not blanket-caught.

Native HTTP, host admission/config snapshots, worker/CLI and real timing acceptance are separate. No real downloader/media/NAS, vendor imports, HTTP/source/runtime execution, product edits or ledger changes by this preparation owner.
