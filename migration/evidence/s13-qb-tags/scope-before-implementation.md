# S13 remaining four — source-first bounded dependency review

Read-only, 2026-10-31. Fixed core `8a83efeacd3e26b82be495e17d50146e31c96a60`; pinned vendor qB 2026.6.0 / observed TR 7.0.11. No source/vendor import, execution, daemon, network, Go command, product or ledger change. Current first-four candidates remain frozen. This is a next-slice contract, not acceptance of these four unimplemented ordinary operations.

## 1. qB delete_torrents_tag: two writes, not old RemoveTag

Core qB lines 304–319: missing client→False. In one try, call `torrents_remove_tags(torrent_hashes=ids,tags=tag)` **then** `torrents_delete_tags(tags=tag)`; any nonthrowing returns are ignored; both complete→True. Either ordinary exception logs `删除种子Tag出错：…` and returns False. First failure prevents the second; second failure does not undo the first. It removes the tag from selected torrents **and deletes the global tag definition**, not torrent files.

Pinned vendor `torrents.py:1870–1945` maps these to `POST torrents/removeTags` (hashes `|`, tags `,`) and `POST torrents/deleteTags` (tags only). Both independently check Web API >=2.3.0 using the current version getter. Default nonraising unsupported-endpoint policy returns None/no selected POST, so even two unsupported no-ops lead to wrapper True. Nullable/string/list preparation and duplicate order are the same fixed `_list2string` dependency already used by S13 AddTags; no new parser/version dependency is needed.

Reuse S03 private client/proof/guard/dispatch, but preserve a **single aggregate mutation fact across both selected writes**. Resetting the proof for write two could incorrectly label a second pre-dispatch failure as zero-effect after write one succeeded. Do not nest `owner.run` while its native mutex is held. A composite internal operation under one run, with below-run fixed request helpers, is the minimal safe shape. Existing no-selected-replay/unknown-effect safeguards remain explicit platform boundaries, not original source rollback semantics.

## 2. qB get_torrent_id_by_tag: nine delayed reads plus that cleanup

Core qB lines 365–399 and complete `get_torrents` 224–254:

* Actual `range(1,10)` means **nine** iterations, each starting with `time.sleep(3)`, including the first. Full exhaustion has 27 seconds of deliberate delay plus request time; no tenth attempt, immediate first read, or extra final sleep.
* Helper calls `get_torrents(status=status,tags=tags)` with no IDs. Its returned error flag is discarded. The complete get_torrents wrapper issues `torrents_info`, optionally filters **all** rows by set-subset tags, and catches query/tag errors to `([],True)` with its original log. A later malformed tag can invalidate an earlier matching row; do not stop filtering as soon as one candidate appears.
* Truthy string tags split on comma without trimming wanted values. Returned tag text splits and strips each element; list tags are used as supplied. Falsey tags bypass the tag filter. Row order is daemon/vendor order, with no new sort.
* Helper returns first selected row's `.get("hash")`; absent/null hash→None. Poll continues **only when `is None`**. Empty string, false or numeric values are not converted to None by the original function. Normal useful output is string/null, but a broader JSON decoder must not silently replace these observed distinctions with a nonempty/hex-only rule. `.get` on a nonmapping first row occurs outside the helper's small try and can propagate; no blanket catch is justified.
* Once non-None is observed, call the complete two-write `delete_torrents_tag`, ignore its source bool, break and return that same hash. A source cleanup False does not erase the read result. Retained platform cancellation/authority/unknown-effect errors must not be silently swallowed as this source False.

Vendor info is **POST** `torrents/info`, not download-page GET. Only statuses `paused/stopped/resumed/running` cause a fresh version lookup and the 2.11 alias mapping; status None omits `filter`. No server-side tag parameter is substituted for the source's client-side all-tags predicate.

Existing `IYUUClient.PollByTag/FindByTag/RemoveTag` cannot implement this contract unchanged: single nonempty comma-free tag, no supplied status, valid-hash narrowing, per-row malformed handling and only one cleanup write differ. Existing `IYUUOptions.Sleep` gives a cancellable production timer (nil default) and is a useful narrow timing dependency; don't globally shorten the delays or alter worker protocol/timeouts. Future owned timing evidence can record requested durations and one real ordinary call, without nine real external requests or a new timing framework.

## 3. TR set_torrent_tag: supplied old labels, set semantics

Core TR 168–181: missing client or falsey ids or falsey tags→False/no call. Inside try compute `list(set((org_tags or []) + tags))`, then `change_torrent(labels=…,ids=ids)`; nonthrowing→True; exception logs `设置种子标签出错：…` and False. The method itself **does not fetch old labels**. Module wrapper `transmission/__init__.py:490–504` separately obtains them; do not add that query to this low-level method. Empty tags do not clear labels.

Useful ordinary domain is string/list IDs accepted by the pinned TR selector and lists of strings for tags/old tags. Set deduplication is not stable insertion-order dedup or sorting. Python string-set iteration is process/hash-seed dependent; this review does not invent a portable exact array order or declare sorted output equivalent. A source component capture must preserve the actual supplied lists and actual raw order, while the acceptance rule must explicitly separate set membership from any claimed wire-order equality. Mixed scalar equality/unhashable inputs must not be generalized from string labels; unsupported broader Python values remain explicit rather than arbitrary object emulation.

Vendor `change_torrent` maps labels into `torrent-set`, uses existing require_ids parsing, and issues an RPC-version warning below 16 but **does not suppress the request**. `_single_str_as_list/list_or_none/remove_unset_value` are already present in the pinned client file. Existing `Client.SetTorrentTags` has valid-hash narrowing and different generic TR dialect/response semantics; reuse private transport/session/proof only, not that entire method. The current S03 session initialization does not expose an independently verified low-version warning/log path; if included in the promised native behavior, it must be preserved or accurately left unresolved, not converted to a hard version gate.

## 4. TR change_torrent: four parameters, conversion before try

Core TR 388–423 is limited to `hash_string`, upload/download/ratio/seed-idle limits. Falsey hash→False before anything. There is **no missing-client guard**; a missing client's call later raises inside try and is logged/caught. All value preparation occurs **outside try**:

| Supplied non-None input | Exact prepared fields |
|---|---|
| upload_limit | uploadLimited=bool(value), uploadLimit=int(value) |
| download_limit | downloadLimited=bool(value), downloadLimit=int(value) |
| ratio_limit | seedRatioMode=1 if truthy else 2; seedRatioLimit=round(float(value),2) if truthy else 0 |
| seeding_time_limit | seedIdleMode=1 if truthy else 2; seedIdleLimit=int(value) if truthy else 0 |

Thus 0, negative numbers, booleans, and numeric strings follow Python truth/conversion, not generic “positive limit” validation; e.g. string `"0"` is truthy before numeric conversion. Conversion errors propagate without the wrapper's caught-call log/False. Finite bounded ordinary numerics/strings can be supported without arbitrary Python constructors. Decimal rounding must match Python binary64 `round(...,2)`, not assume decimal half-up or a multiply/round shortcut. This is a specific small source-observation need, not a new numeric ecosystem.

The call uses camelCase names via vendor **kwargs**, which are merged unchanged after omission of unset named arguments. Do not snake-case them into unrelated fields. All four parameters None means only ids at the wrapper: vendor args empty raises `ValueError("No arguments to set")` before selected HTTP, so source wrapper logs and returns False. Nonempty args use `torrent-set` and existing require_ids rules. No arbitrary kwargs, file priorities, location/tracker operations, or reannounce are included.

## Smallest next complete slice / remaining prerequisites

1. **qB delete_torrents_tag + get_torrent_id_by_tag together** is a useful closed dependency group. Fixed source/vendor inputs are available; no S04/S06 decision or missing native artifact blocks it. Need exact ordinary SDK/host ports, source complete wrapper/helper observations, composite two-write accounting, source-shaped query/filter and nine-delay semantics. Fixed owned protocol cases should distinguish first/second-write failure, None exhaustion vs found result and cleanup result; preserve old consumer credit rather than rerun all IYUU.
2. **TR fixed-limit change_torrent** can proceed independently with the four-value conversion preparation and old TR request/proof. A small original component baseline for conversions/outside-try errors is the actual prerequisite, not missing dependencies.
3. **TR set_torrent_tag** has no missing source/vendor artifact, but exact label-order claims and the below-16 warning path need an explicit bounded acceptance definition before calling the ordinary operation fully source-equivalent. Do not silently replace source set semantics or require an unnecessary preliminary label read.

The common S03 host configuration/scope/admission/guard and fixed independent clients remain reusable; unknown-effect safety remains. Existing completed-query native vendor JSON normalization and tag-pass code are relevant **components**, not a directly reusable completed-query operation: completed filtering must not leak into ordinary polling. No need for full DAO, real downloader, plugin, NAS, new vendor installation, or broad historical suite. No new collector/framework has been authored in this task.
