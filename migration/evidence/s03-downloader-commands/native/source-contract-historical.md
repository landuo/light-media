# S03 fixed native ports: source-first implementation contract

## Conclusion and provenance

**A useful finite native implementation can proceed:** fixed qB/TR `Add`, `Start`, `Recheck`, `DeleteKeepFiles`, with plain bytes / magnet add, ordinary nullable fields, and the selectors below. This is a static contract, not a native runtime PASS. The existing 34-case oracle remains a **MoviePilot wrapper → recording-port** observation; it does not prove these newly recovered vendor transformations.

MoviePilot is fixed at `8a83efeacd3e26b82be495e17d50146e31c96a60`. Its `requirements.in` pins `qbittorrent-api==2026.6.0`, but only constrains `transmission-rpc~=7.0.11`. The retained installed-source record identifies TR **7.0.11**, not every release permitted by that range.

The complete necessary vendor files were absent in the previously identified local environments. Approved public GitHub connector reads recovered exact releases:

* `rmartin16/qbittorrent-api`, tag `v2026.6.0` → commit `0bd26a9f44c113a315146428c3fd78408bc2c985`.
* `trim21/transmission-rpc`, tag `v7.0.11` → annotated tag `78eb2f7696fbc30dd3210f7e462f9dd3a10ec395` → commit `8cd8c22353adf85795a62687044526c5537fa516`.

All 11 saved files match the pinned Git tree's blob SHA-1 and byte length. qB `torrents.py` SHA-256 `d087e5ef72ee748a8af160fd7f4fdbf119d41df6b1fdf2fe1d50509a3a0c2e26` and TR `torrent.py` `8978c8f8171b8758716a9b8d64b03ce8118ea576ee2dd40ddd15b07b3f0a04ee` also match the retained installed-source record. The tag/API metadata and full bodies are local audit inputs; exact URLs and hashes are in `input-bindings.json`. This does **not** recover the entire historical installed distribution or prove its full requests environment.

Acquisition correction is retained explicitly: the initial shell-double-quoted local transfer produced incorrect draft bytes and failed byte/hash checks. They were not used for this contract. Correctly quoted writes replaced those audit drafts; final bytes all match Git blobs. Intermediate failed-transfer stderr was not retained, so no claim is made about every shell interpolation outcome. No vendor Python import/execution, installation, daemon request, source probe, or Go execution was invoked by this review.

## 1. qB selectors and controls

Source wrapper: `app/modules/qbittorrent/qbittorrent.py`, `start_torrents`, `recheck_torrents`, `delete_torrents`. Vendor: `vendor-qb/request.py:410–420`, `vendor-qb/torrents.py:1080–1150`.

* `_list2string` joins any non-string iterable by `str(item)` with `|`; a scalar string passes through unchanged. For the first native subset support `None`, a string, or an ordered list of ordinary scalar IDs; do not lowercase, validate as only 40/64-hex, deduplicate, sort, trim, or perform an existence query. Nested containers / ordered mapping selectors need an explicit pending boundary unless separately implemented; do not silently reinterpret them.
* `None` means the `hashes` form field is omitted. Empty string and empty list yield a present empty `hashes` field. Literal `all` stays `all`. These are distinct source preparations; do not claim what a real daemon does with empty selection without daemon evidence.
* Wrapper start/recheck do not reject falsey IDs before the vendor. Wrapper delete does: falsey IDs return `False` without the vendor call. Preserve this at the leaf layer.
* Start is the vendor `torrents_resume = torrents_start` alias. It reads `GET app/webapiVersion`, parses with `packaging.version.Version`, and uses `POST torrents/start` for `>=2.11.0`, else `POST torrents/resume`. Recheck uses `POST torrents/recheck`. Do not guess from application major version or probe one mutating endpoint then fall back to another.
* `app_web_api_version()` itself is a GET with no cache in the selected method. The existing target `webVersionLocked` cache must not be assumed to reproduce repeated source version reads. Reuse its version parser, or do a fresh read for each port operation; do not silently inherit stale-version routing.
* Delete uses `POST torrents/delete`, `deleteFiles=bool(False)`. The ordinary API must expose **no true option**. Requests form preparation writes Python `False`, not a newly invented deletion flag. Source wrapper returns `True` after any non-throwing vendor return, including `None`; do not require an `Ok.` response body for controls.

## 2. qB plain add and raw result

Source: wrapper lines 401–475; vendor `torrents_add` and `_normalize_torrent_files`, `request._format_payload`; local fixed requests `RequestEncodingMixin` used only as static preparation evidence.

* Leaf already short-circuits missing client / falsey content to `(False, [])`. String content becomes `urls`; byte content becomes one file. Do not require an infohash, valid bencode, absolute save directory, nonempty tags, or a duplicate preflight absent from this source call.
* First usable native subset: bytes and magnet string, Cookie `None`, nullable bool paused, nullable ordinary string path/category, string or ordered string-list tags, and the source-configured auto-management/sequential/first-last flags. Unknown native kwargs must remain explicit pending boundaries, not ignored. Ordinary URL + non-nil Cookie / global cookie synchronization remain separate pending source branches.
* Wrapper falsey `download_dir` and tags become `None`; category / `ignore_category_check` logic is already in the leaf. The native port consumes the resulting `QBAddCall`, not a stricter interactive request.
* Vendor always reads/parses Web API version before assembling add data. With `is_stopped` absent, **source default `is_paused=False` yields `False or None → None`**, so both `paused` and `stopped` are omitted. Explicit `True` sends both fields as `True`; explicit `None` omits both. Do not turn default False into present false fields, and do not send only one version-dependent spelling.
* `urls` are joined by newline; tags by comma. `None` fields are omitted, but present empty strings remain present. Byte add is multipart with one file field `torrent__0` and fallback filename `torrent__0`; its content is the supplied bytes unchanged. Without files, the source uses ordinary form encoding. Python bool form text is `True` / `False`, not JSON lowercase. Multipart random boundary identity is not a required fixed value.
* qB does **not** query added tasks to manufacture a result. Vendor tries `TorrentsAddedMetadata(resp.json())`; only JSON decode failure falls back to `str(resp.text)`. `Dictionary` normalizes mappings and maps falsey data to `{}`. For the finite normal domain preserve text or mapping values (including unknown JSON fields and numbers) for the already-tested source parser. Do not treat a valid JSON scalar string as the same path as non-JSON plaintext, and do not pretend every exotic JSON shape has been proven.
* The wrapper derives `Success` and IDs from that vendor result, including case-sensitive `Ok`, counts and truthy IDs. Its two-element tuple remains source-truthy even when its first element is false. Never substitute an interactive `Hash/Duplicate` receipt for this tuple.

## 3. TR selectors and controls

Vendor `client.py:54–78,235–260,466–520` is decisive; these transformations were not exercised by the recording port oracle.

* Valid scalar IDs are nonnegative Python integers, or **exactly 40 lowercase hexadecimal characters**. No lowercase normalization, trimming, 64-character hash acceptance, or string-to-number conversion. Python bool is an int subclass; if included in the finite ordinary JSON domain preserve that fact rather than accidentally using Go's type distinction. Keep arbitrary numeric magnitudes / noninteger floats outside the proven subset unless explicitly supported.
* `None → []`; list/tuple preserves order and duplicates and parses each element. Literal scalar `recently-active` is preserved as a string. The same text inside a list is not a valid 40-character hash. `all` is invalid; uppercase hash is invalid.
* Start, verify and remove all call `_request(... require_ids=True)`. Thus `None` / empty list for start/recheck raises vendor `ValueError` **before HTTP**, then the MoviePilot wrapper logs and returns `False`. It is not all-torrents. Delete falsey values are already short-circuited by the wrapper. In particular scalar `0` can select a start/recheck ID but is falsey for wrapper delete; `[0]` is not falsey.
* Fixed methods are legacy `torrent-start`, `torrent-verify`, `torrent-remove`. Start does not request `torrent-start-now`. Remove sets `delete-local-data: false` and exposes no true option. Non-throwing control vendor returns are ignored by the wrapper, which returns true.

## 4. TR plain add and returned object

Vendor `client.py:376–464`, `utils.py:_try_read_torrent`, `torrent.py:252–278`, `types.py:Container`.

* `None` raises locally; bytes become base64 `metainfo` without bencode/infohash validation. Empty bytes raise locally. Magnet string becomes `filename` unchanged. The source wrapper does not have qB's empty-content guard; do not generalize it across kinds. URL strings and local file-like/Path branches are distinct; the first subset need not expose those pending branches.
* Drop only `None` values. Unlike qB, empty `download-dir` remains present, default `paused=False` remains JSON false, and empty labels remain `[]`. A string label becomes one list item, not characters. Non-nil labels trigger a vendor RPC-version warning, not a hard rejection. Cookie None is omitted in the first subset.
* Source library 7.0.11 sends the legacy JSON envelope, no modern negotiation and no generated tag in `_request`. A target modern/legacy session adapter is an existing platform compatibility mechanism, **not byte-identical source wire**. If reused, limit method/argument aliases to these fixed operations and preserve raw returned field names appropriately.
* On success the vendor chooses `torrent-added` when that key exists, otherwise `torrent-duplicate`; falsey chosen data is an error (no fallback from empty added to duplicate). It constructs one `Torrent(fields=item)` and returns it. Constructor requires an `id` key, not a prevalidated 40-character hash and not a complete torrent-get row. There is **no follow-up torrent-get**.
* Preserve the returned raw fields and the source `hashString` / `hash_string` access contract. A missing hash field may fail only when accessed; do not invent hash/name/default fields or silently drop a source-valid object. This is a finite ordinary JSON view of the real model, not every lazy Torrent property/method. Exact Python object identity cannot cross the SDK JSON wire; same-object behavior remains meaningful inside the pure port layer only.

## 5. Smallest reusable target construction

The proposed trusted App factory `newOriginalDownloaderCommandNative(configured, guard) → (QBC, TRC, close, error)` is an appropriate fixed interface. Endpoints, credentials and guard come from the current configured downloader snapshot, never worker-provided URLs or kwargs.

There are two separate target implementations: `internal/download.Client` and the `download` package at `internal/plugins/iyuuautoseed/native`. They are not the same state object. The latter already backs ordinary task/query bridges and is the smaller suitable reuse point for its private machinery:

* `NewIYUUClient` installs configured-origin Bearer, current-call guard, cookie/basic-auth session, mutex and the private monotonic dispatch marker; `Close` closes its owned idle connections. Retain these behaviors. The factory must require a non-nil trusted guard.
* Reuse `nativeLock`, selected request helpers, read-only authentication/session negotiation, the source version parser, and per-call `beginMutation` / `finish` proof. Add fixed leaf-facing methods, not arbitrary RPC/URL access.
* Do **not** call IYUU `AddPaused`, existing `Start/Recheck` hash-only wrappers, `TransferDeleteTask` single-hash wrapper, or interactive `Client.Add/Control` as behavioral substitutes. They respectively force paused/add validations, reject source selectors, normalize hashes, add existence/duplicate work, or reduce replies.
* TR `trSelectedRPC` currently returns recursively normalized legacy fields. Do not use that normalized object blindly as the original Torrent fields; a narrow raw-response option/fixed helper is needed to preserve `hashString` and unknown nested keys. Existing alias normalization can otherwise destroy a source-returned field-name distinction.
* Preserve every-dispatch guard, configuration/identity checks before and after calls, no credential redirect, existing proxy environment and current target third-party SDK TLS verification. **The fixed qB source constructor explicitly sets `VERIFY_WEBUI_CERTIFICATE=False` (`qbittorrent.py:203`); current target strict verification is therefore a real pending difference, not the qB source default.** The RequestUtils noverify authorization does not automatically cover this separate qB SDK constructor; do not change TLS here without the specific authorization. Preserve 16 MiB / other explicit resource bounds as target safety differences, not unlimited source equivalence.

### Error boundary is part of this contract

Local vendor-shaped selector/type failure before dispatch can be a `SourceException`, so the wrapper performs its source log and False/None fallback. A response that is a definite source-level negative value remains a value. Authority/revocation/cancellation are **not** caught source business exceptions. Once the selected mutation was delegated, transport/body/ambiguous-response failures remain unknown-effect boundary errors; do not retry or downgrade them to harmless False. Preserve the exact private dispatch proof, not error-string classification. The original qB vendor retries certain transport/5xx failures and source wrappers catch broad exceptions; the platform's no-replay/unknown-effect policy is an intentional retained safety difference and must be stated, not called complete source catch equivalence.

## 6. Minimal future owned-fixture gates (not executed here)

One bounded control gate per kind can cover the selectors above and zero-wire invalid/empty TR cases; qB start old/new API versions and fixed false deletion flag. One add gate per kind can cover byte/magnet, paused/default/None distinction, nullable path/labels, raw qB text+mapping, TR added+duplicate raw fields, and no hidden existence lookup. One shared guard/unknown-effect gate can use the existing private dispatch proof and assert no second selected POST, current cancellation/config revocation, credentials and Close. Reuse existing source leaf oracle as wrapper-only evidence; do not rerun historical unrelated suites or claim a real daemon test. Any actual source preparation collector must be separately frozen and authorized; none was run by this review.

Pending remains explicit: ordinary URL with Cookie/global synchronization, unrestricted kwargs and Python objects, complete TR model properties, full historical requests runtime, and real daemon behavior. These do not prevent implementing and proving the stated useful fixed subset, but cannot be silently advertised as the entire S03 parent.
