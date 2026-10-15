# Explicit isolated M0 NFO experiment CLI

This is an explicitly authorized isolated M0 experiment CLI, not a production fnOS integration or general writer. Its source was promoted from work/_fnos-experiment after the bounded live experiment.
Credentials are one JSON object on stdin, never CLI arguments:

```json
{"host":"https://synthetic.invalid/v","username":"…","password":"…","experiment_id":"00000000-0000-4000-8000-000000000001","files_prepared":true,"fixture_dir":"work/new-fixtures"}
```

Build from repository root: `go build -o work/fnos-nfo-experiment ./experiments/fnos-nfo`.
Default is a zero-network dry run. Apply requires `--apply --action prepare|scan|observe|cleanup|diagnose|image_shape|images|inspect_files`.
Keep working directory at repository root so receipts remain discoverable.

1. Separately prepare exactly `/vol2/1000/media/light-media-m0-test-UUID` with an exclusive creation operation. Verify no symlinks, no physical aliases into existing libraries, and no existing media reuse. Use only newly generated synthetic clips/NFO, UUID-prefixed titles/actors/custom IDs. Do not supply actual movie/person metadata-provider IDs.
2. `prepare` saves a credential-free baseline receipt before PUT `/mdb/create`. It creates a new Mix library with local NFO preference enabled, skip size zero, subtitles/progress thumbnails disabled. It checks the resulting identity and local-NFO/skip settings. A create request may itself trigger a scan of this new library.
3. `observe` first verifies ownership. It starts with at most eight root items and traverses TV → Season → Episode using only GUIDs returned under that owned library. Before dependent reads, each detail must assert `ancestor_guid == owned library GUID`, the actual `parent_guid == verified traversal parent`, and a valid type at that depth; missing/mismatched fields fail closed. Output parent/ancestor fields now come from the backend, with `scope_verified=true` only after checks. The complete tree is limited to ten nodes and three children/parent, with twenty persons/node and the same total request limit. It does not claim NFO success automatically. Check the actual synthetic values. Custom unique-ID objects are not fully inspected. Image-field presence/count and reported dimensions are included, but image bytes are not fetched; `image_bytes_verified` remains false.
4. If needed, `scan` requests only the owned library and exact synthetic root, once. It never requests scanall. `scan_accepted` is not scan-completion evidence.
5. `cleanup` checks ownership again, DELETEs only the owned new GUID, and verifies its absence and unchanged original library names/categories/directory lists. The runner never deletes remote files. Remove only independently verified self-created synthetic files after library cleanup.

The local receipt is `work/fnos-nfo-experiment-UUID.json` (0600). Do not remove it until cleanup is verified. After a lost create response, use observe or cleanup, never repeat prepare. A lost delete response can be reconciled by cleanup without another DELETE. Per-experiment exclusive lock rejects concurrent runners; a process crash may require inspecting/removing its stale `.lock` manually. Never overwrite a receipt to force a new create.

Safety scope: lexical directory overlap is checked against every returned library root, including parent and child overlap. Physical path/symlink checks belong to the separate remote file preparation operation. No existing library is mutated; no item deletion endpoint, global settings, or arbitrary endpoint argument is available. UUID names also reduce accidental cloud matching/actor de-duplication; prefer-local-NFO does not prove cloud fallback is disabled. Cleanup verifies library configuration only and does not claim removal of every shared person/cache record.

Bounds: 64 KiB stdin, 32 requests/action, 12 seconds/request, 90 seconds/action, 2 MiB/body. All redirects rejected, TLS defaults preserved. Output excludes credentials, response bodies, full remote URLs, and arbitrary response properties. Body business errors become fixed error codes. Mutations' code=0 is followed by read-back except scan, which is explicitly only accepted.

Protocol evidence: installed public `work/fnos-public-mdb-view.js`, `fz` create-body function; `Aw.noSkip=0`; `KT` deletes library using only guid; `qT` scans a guid and optional dir_list. `work/fnos-public-mdb-client.js` exports `kt→Uc` (prefer NFO), `Tt→qc` (thumbnail), `Et→Kc` (subtitle), each Enabled=1/Disabled=0. Library routes PUT `/mdb/create`, DELETE `/mdb/:guid`, POST `/mdb/scan/:guid`. Item GET `/item/:guid`; persons POST `/person/list/:guid` with page/page_size. `lan` and `subtitle_lan` are omitted when undefined by the UI body builder. No global preferences are changed.

Synthetic verification: `go test -race ./experiments/fnos-nfo`, `go vet ./experiments/fnos-nfo`. Tests cover full create/scan/observe/delete lifecycle, read-back, original-library invariance, scoped signatures, redirects, null/missing/business/oversized responses, credential and URL/newline projection, root overlaps, existing GUID rejection, and receipt scope. Actual NAS execution is performed separately by the parent task, not these tests.

`diagnose` is read-only after login: it lists only names prefixed by this exact experiment UUID, reports name/category/dirs/GUID, and does not change the receipt. Directory equality accepts trailing/repeated separators but rejects dot path components. It preserves original GUID and overlap safeguards.

`inspect_files` is a read-only diagnostic for an existing receipt with a claimed
owned GUID. It verifies that library and the complete bounded `observe` tree
before POST `/item/getEditDetail` with only `{item_guid}` for at most three
current Movie/Episode items. The route and request body are evidenced by the
installed public client/view bundles, not guessed mutation endpoints. Neither
the receipt nor library is changed; the usual per-experiment local lock still
serializes runners. `observe` now describes evidence neutrally, irrespective of
whether its prepared NFO used synthetic custom IDs or separately verified
standard source IDs. No observation automatically proves those IDs were read.
If edit detail has no unique usable path, the action re-GETs only that already
known `/item/:guid`, verifies its kind/ancestor/parent/depth again, and inspects
only the returned `media_stream` shape. A foreign identity or bad edit path
fails immediately and cannot invoke this fallback. Both endpoint shapes remain
visible. If that fresh media stream also has no unique path, the final read is
GET `/media/itemfile/:guid` for the same scoped item. The installed client's
`W.files` route and view `QB(itemGuid)` show a bounded array of `file_path`
strings; this is an item attachment read, not library GUID or playback access.
At most four records are inspected, with present ownership/parent assertions
checked and only one unambiguous attachment eligible. All three endpoint shapes
remain visible. The representative full fallback uses exactly the existing
32-request cap; it does not increase the budget. Missing fields or multiple
attachments remain unverified rather than supplying an inferred path.

The diagnostic reports only fixed allowed field names, value types and bounded
container shapes. It never returns the full edit detail, unknown string values,
headers, URLs or rejected paths. Present item/library identity fields must match
the scoped target, including nested ancestor/mdb/item assertions. Unknown shape
or absent paths remains `unverified` with nonzero CLI completion. A foreign
library/target, traversal/URL, path outside the own root, noncanonical filename
or conflicting binding stops the action. Generic `path`/`file_path` and bounded
known file-container selectors are **diagnostic candidates**, not a claim that
this fnOS version has returned any such field; live field evidence is separate.

Only an actual backend-returned absolute string equal to the owned root plus
one of the three canonical paths below may form a binding:

- `夜王 (2026)/夜王 (2026).mkv` for Movie;
- `生活大爆炸/Season 7/生活大爆炸 S07E17.mkv` for Episode;
- `诛仙/Season 4/诛仙 S04E05.mkv` for Episode.

`file_evidence` has `{experiment_id,owned_guid,root,bindings}` and each binding
has `{item_guid,relative_path,actual_path}`. `actual_path` is never generated from
a GUID, basename or local manifest. Exactly three distinct bindings are needed
for `file_associations_verified=true`; partial bindings do not satisfy the
offline comparator. A lexical backend path association does not establish
physical symlink/inode identity, file contents, NFO field matches or global
provider-cache isolation. Separately prepared file manifests and comparisons
must supply those other facts.

Scoped item observations additionally retain `tmdb_id`, `source` and `source_id`
only when actually returned and accepted by narrow value rules: known source
namespaces, positive bounded numeric IDs, or short known opaque ID spellings.
Numbers/strings and opaque prefixes are preserved, never converted into an
assumed TMDB type/ID. Other values produce only a type state.

`image_shape` reads only three owned root items and reports fixed local route templates, field types, segment counts and extensions. It does not expose raw image references. `images` additionally fetches at most six images (poster/backdrop per item), only from the same origin under `/v/api/v1/sys/img/`. Remote-proxy routes, URL user info/query/fragment, encoded separators, traversal and redirects are refused. Images are bounded to 2 MiB and decoded as JPEG/PNG under dimension/pixel limits. `fixture_dir` selects a new local fixture directory under work (default `work/nfo-live-fixture-v2`); NFO XML supplies the exact type/title identity. `posters` must match that work’s poster file, and `backdrops` must match its fanart file in SHA-256, format and dimensions. Unknown/missing/duplicate work identities, missing image fields, mismatches or equal hashes across fixture roles fail. Exactly one .jpg or .png is allowed per role. The verifier requires three root works and six distinct role hashes. No width/resize parameter is supplied. `observe` image_bytes_verified=false applies only to its metadata inspection; the separate images action supplies byte verification.

Installed-client evidence for image URLs: `fnos-public-mdb-client.js` exports E→mh=`/v/api/v1/sys/img`; view qE/XE append local references, and the client excludes this image route from authx signing. The experiment refuses the separate remote `sys/rimg` route.

Image synthetic tests cover exact byte/hash/dimension matches, direct external targets, scheme changes, remote routes, URL credentials/query/fragment, dot/encoded traversal and redirects. Actual live observations and limitations are recorded in [docs/nfo-verification-plan.md](../../docs/nfo-verification-plan.md). In the historical fixture, poster/fanart share bytes within each work, so matches establish the correct work group but not poster/backdrop role distinction.

The new ownership and distinct-image checks have synthetic replay coverage; they were not retroactively applied to the first live run. A new UUID live run is required to establish actual ancestor/parent field values and poster/background role distinction. Historical legacy fixtures remain reproducible by the fixture generator but are deliberately refused for the new role-distinction verification. No provider-ID acceptance is implied by scope or image verification.

The local `images` fixture catalog accepts either a complete historical `namespaced` publishing layout or the explicit `canonical` layout generated with `experiments/nfo-fixtures --layout canonical`. It rejects mixed layouts and arbitrary extra directories. Layout detection does not alter identity: type plus UUID-namespaced XML title binds each work, and each `posters`/`backdrops` field must match that work's exact image hash, decoded format and dimensions. Canonical paths alone do not establish NFO field success; run the separate offline comparator against a fresh observation. The old layout and legacy evidence are retained.
