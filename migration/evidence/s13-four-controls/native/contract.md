# S13 four native ports — candidate 01, not executed

Authored only in this audit directory. Intended destinations are `internal/plugins/iyuuautoseed/native/shared_downloader_four_commands.go` and `_test.go`; their package is `download`. No old client, factory, eight S03 algorithms, App hook, TLS policy, environment, or ledger is changed. This author's earlier independent source-component acceptance does **not** approve this native implementation; a separate reviewer is required.

## Exact reuse and source mapping

The concrete receivers remain `*sharedQBCommands` / `*sharedTRCommands`, returned through the old QBC/TRC interfaces by `NewSharedDownloaderCommands`. Optional new leaf interfaces assert `AddTags(ctx, ids, tags)`, `SetCategory(ctx, ids, category)`, and `Stop(ctx, ids)`, each `(any, error)`. The native ports return nil after nonthrowing selected calls; only the original leaf determines None/bool and catches explicit source exceptions.

* qB tags: fixed `torrents_add_tags` (pinned qbittorrent-api 2026.6.0). Prepare hashes with `|` and tags with `,`, using the retained scalar renderer, not the old Add-only string-tags restriction. Then fresh Web API version GET; versions below 2.3.0 produce nil without selected POST under the source constructor's default nonraising endpoint gate. Supported versions issue only `POST torrents/addTags`. Null omits fields; empty list emits a present empty field. Order/duplicates remain. This is add-tags, not replace-tags.
* qB category: hashes preparation and nullable category form field, then `POST torrents/setCategory`, with no version lookup. The source leaf, not this port, handles falsey hash/no-call and falsey category→empty string. String/finite scalar category is supported; structured category encoding remains an explicit existing BoundaryError, not fabricated source rejection.
* qB stop: pinned `torrents_pause` is an alias of `torrents_stop`. Fresh version lookup precedes selector preparation. Web API >=2.11.0 selects `torrents/stop`, older selects `torrents/pause`. Existing version parser handles its already retained finite packaging-version comparison. Vendor `Application.web_api_version` calls the underlying getter; `ClientCache` stores the client, not the version response.
* TR stop: reuse `sharedTRCommands.control(ctx, "torrent-stop", ids)` unchanged. Its original parser accepts nonnegative integer IDs (including Python-bool-compatible JSON bool), lowercase 40-hex hash, or exact scalar `recently-active`; nil/empty IDs yield SourceException ValueError before any RPC. Then existing readonly session-get negotiation, fixed legacy envelope and selected single POST. No new model or generic RPC.

Fixed vendor commits and file hashes are bound by `candidate01.json` and the retained S03 acquisition contract. The core source pins qB ==2026.6.0; TR ~=7.0.11 is distinguished from the specifically observed/retained 7.0.11 source. No vendor was imported or executed. The prior acquisition shell incident's side effects remain unknown; final content hashes are not proof of absent side effects.

## Safety and limits retained

All four routes use the existing private `owner.run`, native mutex, pre/post configuration guard, `qbSelected` or TR selected dispatch and monotonic mutation proof. Context/authority/Boundary failures remain errors; any already-delegated mutation failure remains unknown effect and is not retried or converted to source False/None. This is the established platform safeguard, not a claim of identical vendor retry/catch behavior. Old qB-only constructor TLS exception and TR strict TLS/trust_env=False stay byte-identical; no global TLS/proxy/CA changes.

This finite JSON/string/list subset does not implement arbitrary Python iterables, ordered-map-as-selector semantics, structured category repetition, new native kwargs, debug-log plumbing, real daemon state, or downloaded media. Known native failure boundaries are explicit, not silent success. Unsupported-version tag nil is the actual vendor no-op behavior, not a mutation receipt.

## Prepared owned tests (not run by author)

Exactly three roots:

1. `TestSharedDownloaderFourQBWireAndVersions`: owned HTTP sequence covering 2.3 and 2.11 boundaries, null/empty distinction, ordered scalar joins, exact category field, ignored response body and no unsupported tag POST.
2. `TestSharedDownloaderFourTRStopSelectors`: invalid IDs cause zero HTTP, then actual owned session negotiation, fixed stop envelopes and selector preservation.
3. `TestSharedDownloaderFourGuardAndUnknown`: four selected ports, pre-guard/pre-cancel no wire, fixed 500 selected once and retained unknown effect.

Only owned `httptest` responders, no forwarding/CONNECT, daemon, global transport or environment mutation, hidden compiler/process, source Python, or fresh network locator. `gofmt` only ran on these two audit candidates with the pinned formatter; no Go build/test/list has run. Original 17-case raw is independent leaf evidence, not reused as fabricated native protocol output. Related old S03 roots and host integration remain coordinator decisions.
