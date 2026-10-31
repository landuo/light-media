# S12 material source-component actual readback

**Accepted as a finite source oracle, not as end-to-end S12 acceptance.** The coordinator actually ran the frozen collector with `-I -S -B`, 2026-10-31 18:15:22.919125–18:15:23.108021 UTC, exit 0; stderr is empty. Raw stdout is 43,162 bytes, SHA-256 `6e654f82a7e3099eadcde59b38ab9b12c7a8a6b2cc5e1e3909a4a7e1d528e5c6`. No reviewer execution or rerun.

## Independent checks

- All 5 declared inputs match the frozen manifest and current bytes. Both complete original methods (including the inner redirect function) and both enums have exact source-fragment and AST hashes matching raw output and the earlier static review. This is not a complete interpreter/stdlib dependency map.
- All 19 full input records and synthetic settings equal the frozen owned-cases document, in the original order. Outcomes, ordered traces and notices were read from raw, not inferred from the owner's report.
- **17 tuple returns: 7 bytes, 3 strings, 7 None; 2 recorded source exceptions.** The bad-base64 case raises `binascii.Error`; the bad-JSON case raises `json.decoder.JSONDecodeError`. Both have empty dependency traces/notices. Exit 0 means the collector completed, not 19 successful downloads.

## Observable facts available to the target replay

1. Empty enclosure and magnet short-circuit without helper/request/notice. A helper string, including the empty string, returns as string before the empty-content check; folder/files are reset. Byte returns preserve the exact controlled bytes and ordered folder/files.
2. Empty bytes and None each record a Manual notification after the error log. The channel-bearing case retains `source=OwnedSource` and integer userid 17; without channel the notification source is None. Early inability to resolve a URL instead logs and returns None without a notification.
3. Indirect requests record the configured cookie opt-in, nonempty header map, descriptor proxy choice, and query `params`. Only lowercase `get` uses GET; uppercase `GET` uses POST with query parameters, not a body. False response / false success / false nested result stop before the helper.
4. Every indirect second-stage helper call has cookie None and cache_invalid false, including `[]tail`. Direct calls preserve the input cookie and cache_invalid true. Final helper UA is site UA or synthetic default; final proxy is site_proxy, distinct from descriptor proxy.
5. The raw output fixes exact result-path query encoding and relative/absolute base normalization, including retained path/query/fragment and the invalid-base unchanged case. Preserve these returned URLs rather than reconstructing expectations from Go.

## Oracle handoff and limits

Use the complete raw JSON as the canonical oracle, or an explicitly documented extraction retaining all 19 cases with full inputs/outcome/trace/notices plus source/driver/manifest/settings/node/dependency metadata. Keep `$bytes`, enum identity/value, tuple/type tags, None vs empty string, list order, exception module/type/message/args and trace order. Raw and frozen inputs must remain immutable; representation adaptations must be explicit rather than rewritten expected Go values.

RequestUtils, Response, TorrentHelper, Notification and post_message are **recording dependencies**. This run proves selected-source control flow, stdlib decode/URL operations and call arguments; it does not prove HTTP, proxy or cookie transmission, real parser/cache behavior, Notification schema validation/delivery, DAO/downloader behavior, or target equivalence. The synthetic bytes are not claimed to be a valid torrent. Module absence checks are not syscall tracing. The separately sealed parser source/baseline/fix/after evidence is unchanged and is not rerun here.
