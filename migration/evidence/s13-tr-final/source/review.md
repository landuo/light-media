# TR final two leaves — actual source observation and minimal implementation contract

**ACCEPT: original selected-method / recording-port evidence only.** No target implementation, native RPC, real downloader or whole S13 completion is accepted by this report.

## Actual execution and independent readback

The coordinator ran the reviewed collector once using the pinned Python with `-I -S -B`, 2026-11-01 00:34:04.867469–00:34:05.363441 UTC. Recorded command names this review's pre-execution seal. `exit` and `result.json` both report 0; stderr is empty. The parent reports actual tool `36e991`, exit 0; that tool identity is distinguished from the on-disk command/result receipts (no separate tool-transcription file was present at readback).

Original raw output is **32,975 bytes**, SHA `1c3a9652c19898ed1aa98d63b1650f07587dee87654384490047e7872f62671e`, at `evidence/s13-tr-final-source01/stdout.json`. It is not rewritten or normalized.

Independently checked all 15 manifest inputs and all eight preparation entries against current bytes; zero drift. Reparsed only the fixed source to verify both complete original nodes and containing-class AST hashes; actual node descriptors equal the prepared originals. All 23 actual case inputs match the fixed cases, including scalar types; source arguments remain unchanged. Every captured kwargs value's Python type matches its declared trace type.

The actual result is **9 True returns + 10 False returns + 4 uncaught ValueError observations**, not 23 business successes. There are 12 recording calls, nine nonthrowing replies, three supplied dependency exceptions and six ERROR-log observations. No second source execution, vendor import, Go gate or network operation was performed for this review.

## Minimal source-faithful two-operation contract

### `set_torrent_tag(ids, tags, org_tags=None)`

* Missing client, falsey IDs or falsey tags returns False without a dependency call or error log. Empty tags do not clear labels.
* Otherwise the original try contains list concatenation and `set` deduplication, then exactly one `change_torrent(labels=..., ids=ids)` call. No retrieval of existing labels is performed. Only the caller-supplied old labels are merged.
* A nonthrowing dependency reply is ignored: the observed False port reply still yields source True. An ordinary concat/unhashable/call exception logs `设置种子标签出错：` plus its message and returns False.
* The actual merge-case label sequence is `["owned-new", "owned-retained", "owned-old"]`; this run-local list is retained intact. For the finite **string-only** set property, later comparisons may use exact case-sensitive membership and unique cardinality, with target permutations recorded explicitly. Target duplicates, missing/extra/trimmed/case-folded labels remain failures. All other fields and ordered effects remain exact. This is not a claim of wire-array order equality or real-daemon order insensitivity.

### `change_torrent(hash_string, upload_limit=None, download_limit=None, ratio_limit=None, seeding_time_limit=None)`

* A falsey hash returns False before conversions. There is no corresponding early missing-client check: an absent client fails inside the final try and is logged/returned False.
* Preserve supplied values and defaults. None omits that setting. Explicit zero is not None. Flags and modes derive from **the original value's truthiness before conversion**, not from the converted number: string `"0"` gives an enabled flag/mode with numeric zero, unlike number 0.
* Preserve original camelCase fields. Upload/download use bool(original) and int(original); ratio uses mode 1/2 and round(float(original), 2) or integer zero; idle uses mode 1/2 and int(original) or integer zero.
* Actual observations include negative values, booleans and numeric strings. Ratio `2.675` produced float `2.67`, string `"2.685"` produced float `2.69`, and `-2.675` produced `-2.67`. A generic decimal half-up or multiply/round/divide shortcut must not silently substitute for these observations. True ratio produced float `1.0`; falsey numeric zero produced integer `0`. Keep the type trace, even where JSON numeric comparisons need a documented representation rule.
* All four conversion blocks occur **outside** the source try. Each of the four fixed invalid-string conversions raised ValueError with no dependency call and no caught-call ERROR log. These must not become False or an attempted mutation. Preserve the distinction between a genuine source conversion exception and platform validation/authority/cancellation/unknown-effect errors; this source component supplies no permission to swallow the latter.
* Only the final `trc.change_torrent(**change_kwargs)` is caught: any nonthrowing reply yields True; a supplied ordinary call exception logs `设置种子出错：` plus its message and yields False.

## Critical all-None/native seam

The paired all-None cases deliberately send identical `{"ids":"owned-a"}` kwargs to different explicit recording dependencies. A nonthrowing dependency yields True; the supplied `ValueError("No arguments to set")` yields logged False. **The first is not evidence that the actual pinned vendor accepts empty mutation arguments.**

Pinned TR 7.0.11 client text (SHA `5d34047c37bcadb00f3320fd8dbbe46aae6583acb5fd7877ed9a4ca0eea4bf34`, lines 708–742) statically builds settings, merges kwargs unchanged, and raises that ValueError when settings are empty. A source-faithful native implementation therefore must not issue or report a successful empty-settings RPC. The collector did not execute vendor validation. The same distinction applies to the synthetic `owned-a` identifiers: they establish wrapper passthrough, not real vendor ID acceptance. Native selector/version handling must reuse the separately pinned vendor contract rather than infer daemon acceptance from this recording port.

## Exact reuse boundary

Use the complete original raw document as the source fixture; retain metadata, all outcomes, Python types and ordered traces. A later extraction may add provenance but must not replace observed data with Go expectations. Only the pre-agreed string-set comparison may disregard run-local label permutations, without altering the raw evidence. No arbitrary Python objects, mixed-scalar set equality, nonfinite numbers, new kwargs, actual vendor import, constructor, HTTP/RPC, daemon, persistent host or UI has been observed here.

Machine readback: `source01-readback.json` SHA `435c768a8e8173a283458ff05fc8607ce254804a9085077e9760b101702b4824`. It binds every runtime text artifact, all 23 outcomes/traces and the exact prepared/current input checks. No remaining source-collection blocker was found for this declared component layer.
