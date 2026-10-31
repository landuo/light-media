# S13 first four commands — SDK/leaf candidate contract

Prepared only. No target product file changed, no Go build/test or original-source execution by this owner. Fixed MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`: qB `set_torrents_tag` 336–346, `stop_torrents` 490–502, `set_torrent_category` 686–700; TR `stop_torrents` 240–252. This is not all S13 or a downloader/host acceptance claim.

## Four fixed calls

| SDK / leaf method | Capability | Required invoke fields after `op,downloader_id` | Exact reply |
| --- | --- | --- | --- |
| qB `SetTorrentsTag(ctx, ids, tags)` | `downloader.qbittorrent.set_tags` | `ids,tags` | `{"value":null}` |
| qB `SetTorrentCategory(ctx, hashString, category)` | `downloader.qbittorrent.set_category` | `hash_string,category` | `{"value":bool}` |
| qB `StopTorrents(ctx, ids)` | `downloader.qbittorrent.stop` | `ids` | `{"value":bool}` |
| TR `StopTorrents(ctx, ids)` | `downloader.transmission.stop` | `ids` | `{"value":bool}` |

`op` is always `invoke`. Present null differs from a missing argument; no selector trimming, hash-only constraint, deduplication, or SDK truth conversion. Tag SDK/leaf returns only Go `error`: nil denotes the source None path, **not successful mutation**. Missing client, a nonthrowing vendor result (even False), and a caught source exception all return None for tag. Bool methods return False for missing client/caught source exception, True for any nonthrowing vendor return. Category alone has a falsey-hash no-call guard and converts falsey category to `""`. Neither stop wrapper pre-rejects falsey ids. TR vendor require_ids is a later native boundary, not an invented leaf guard.

SDK reuses existing `downloaderCommandReady`, ordinary-value validation, 128 KiB request bound, strict exact response decoder and `downloaderCommandControl` for stops. There is one HostCaller attempt; no retry, upload, rollback or hidden native call. Cancellation, authority/boundary/unknown-effect failures remain errors, not source null/False. All existing S03 wire/product files remain byte-identical.

## Optional host-owned leaf ports

New file adds `QBTagPort.AddTags(ctx,ids,tags)`, `QBCategoryPort.SetCategory(ctx,hashString,category)`, and `StopPort.Stop(ctx,ids)`, each `(any,error)`. They are optional assertions on the existing fixed `QBittorrent.Client` / `Transmission.Client`. **Do not add methods to QBC/TRC or rewrite old fakes.** Nil old client keeps the source fallback. Non-nil client lacking the selected new port is a BoundaryError; it must not be silently treated as a missing source client. Existing source exception/log helper handles only explicit SourceException, retaining cancellation and other boundary errors. No source exception is invented for missing target wiring.

Inputs stay within ordinary JSON; bytes have no role in these four commands. Ports are configured by host, not worker-selected endpoints or credentials. Scope/manifest/current enabled exact downloader config, once-only dispatch, native guards, bounded resource lifetime and qB-only TLS exception remain App/native responsibilities, pending implementation and real gates. No new global registry or permissions.

## Prepared verification and explicit limits

Two SDK roots will record exact four capabilities/frames/None-vs-bool and finite malformed-response/no-retry/admission boundaries. Leaf recording tests will compare the original complete 17-case source raw once actually produced and independently admitted; until then static derivation is **not actual Python observation**. Source supplied recording failures are not actual daemon or vendor-parser evidence. Boundary test checks missing optional ports, source-vs-platform errors and cancellation without any real IO. All raw traces/log prefixes and source result types must remain, not just final values.

No original vendor imports/execution, HTTP, native clients, DB, real downloader/media/NAS, fixture, service or network operation is introduced here. Later host/native/owned-worker evidence and related regressions are separate coordinator gates. Original S03 acquisition incident remains recorded; this component does not resolve its unknown effects.
