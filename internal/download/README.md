# Downloader runtime

`New(Config)` selects qBittorrent 5.x or Transmission. `List`, `Files`, `Add`,
`Control` and `Relocate` execute real downloader operations when called by the
application. Development validation uses isolated `httptest` servers only.

- `Task.ID` is `instance_id:hash`; either this owned ID or an exact hexadecimal
  hash selects one task. Empty selectors, numeric daemon IDs, `all`, multiple
  hashes and other instance IDs are rejected before HTTP.
- `Add` accepts bounded validated torrent bytes and an explicit absolute Unix
  download directory. Raw original info bytes determine identity. Existing tasks
  return `duplicate:true` without applying new state, tags or directory. Adds to
  the same endpoint serialize across clients within this process.
- qBittorrent 5.1 uses `stopped` on add and `start` / `stop` for controls. Adds
  disable auto management and skip-checking. Hybrid torrent lookup checks both
  the v1 SHA-1 ID and the truncated v2 SHA-256 ID because the installed libtorrent
  version determines qB's primary ID. A successful HTTP status still requires
  the add business response and subsequent exact hash observation.
- Transmission negotiates its read-only session operation, using JSON-RPC 2.0
  snake_case on 4.1 and the old envelope / legacy field spelling on older daemons.
  Writes never retry in another dialect. CSRF 409 retries are limited to three
  HTTP attempts per RPC call. Pure v2 torrents are explicitly rejected by this
  Transmission adapter; v1 and hybrid metainfo use its v1 identity.
- Controls are `start`, `pause`, `verify`, `remove_keep_data`. The latter always
  sends `deleteFiles=false` or `delete_local_data=false`. There is no delete-data
  API. `Relocate(ctx, hash, destination)` explicitly requests a file move and is
  separate because `Control` has no destination parameter.
- A 20-second HTTP timeout, 16 MiB response limit, 10,000 task/file limit and
  metainfo parser limits bound work. HTTP redirects are rejected. Credentials
  stay in request bodies/headers and errors omit remote payloads and URLs.
  Network failures are not automatically replayed; callers retain the returned
  add hash and reconcile rather than assuming a failed write never happened.
- `Config.Password` is an input wire field, not automatically redacted by JSON
  serialization. The application must encrypt its persisted configuration and
  exclude credentials from public configuration responses.

Protocol references:

- [qBittorrent 5 API](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-5.0))
- [qBittorrent 5.1.2 add implementation](https://github.com/qbittorrent/qBittorrent/blob/release-5.1.2/src/webui/api/torrentscontroller.cpp)
- [Transmission current RPC](https://github.com/transmission/transmission/blob/main/docs/rpc-spec.md)
- [Transmission 4.0 legacy RPC](https://github.com/transmission/transmission/blob/4.0.6/docs/rpc-spec.md)

Validation: `go test -race ./internal/download` and `go vet ./internal/download`.
