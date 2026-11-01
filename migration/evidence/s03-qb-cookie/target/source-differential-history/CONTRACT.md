# S03 Cookie leaf differential test 01

**Prepared, not compiled or executed.** The only test root is `TestQBCookieOriginalSource17`, package `light-media/internal/plugins/downloadercommands`. No new case matrix, source execution, Go execution, HTTP, daemon or real cookie/download operation. No target-owner directory, ACTIVE or ledger write.

## Evidence and input

The embed is byte-identical actual original `evidence/s03-qb-cookie-source01/stdout.json`, 152283 B, SHA `758df1e970dad525d564dc6732e2153f545dc8a0314fbbfd0b3e846156f02b6a`. Coordinator tool `3d9730` exited 0. Non-author source final seal accepts exactly 17 scenarios / 18 complete original adds / 9 selected nodes. Actual raw records 52 outer protocol calls, 48 returns, four owned pre-effect RuntimeErrors, seven owned store commits, five original logs; 16 True and two False success members, all 18 tuples truthy. These are original observations, not target results.

The test consumes the complete raw directly. Inputs come from each original input step; expected outcome, ordered trace and before/after/final state come from the independently captured original output. It verifies duplicate input copies match. It does not generate oracle data using Go, strip failing events, or substitute a target ID/final-result-only comparison.

## Actual target observation adapter

- One target `QBittorrent` value and one owned recording port/store per source scenario; the last scenario keeps both across its two sequential adds. Target never receives references into the expected raw; ordinary JSON values are deep-copied using `UseNumber`.
- Existing QBC implements all four methods; Start/Recheck/Delete are fatal out-of-scope if reached. URL-cookie calls use `WithAddCookieScope` with the four new primitives `WebAPIVersion`, `Cookies`, `SetCookies`, `Add`. Magnet uses direct old Add only.
- The aggregate injects a private context marker; each primitive and original log callback checks it. Callback activity and single entry/release are checked separately from the original trace (the source recorder has no target framework scope event). These are target wiring assertions, not claimed original concurrency/authorization evidence.
- Every actual primitive appends its call/kwargs. It receives only original recording input controls, returns their values, or returns a **direct** `*SourceException{Class:"RuntimeError",Message:...}` for the original injected ordinary exception. Unknown target transport/policy errors are never fabricated or relabeled as source exceptions; any unexpected Go error fails the gate.
- Successful Set records and commits a copied owned store before recording its return. Injected set failure records an owned pre-effect exception and leaves the store unchanged, exactly matching the original recorder's explicit convention. This is not daemon error atomicity or permission to fall back after uncertain native effects.
- Source return is represented by the actual typed QBAddResult's Success/IDs and its actual `SourceTruthy()` method. A false success member must not change whole-tuple truthiness.

## Exact comparison projection

For every call: full source outcome (tuple/value/type/truth), ordered primitive calls and kwargs, protocol returns and ordinary errors, original log level/message, owned commit events, store-before, store-after and scenario-final store. Whole JSON object key order is normalized by Go's JSON marshal; list order, values, missing object fields, and null versus empty list are preserved. No trace/log/state filtering. No Python object identity or real vendor object/wire identity claim.

The new raw/test are intended at `internal/plugins/downloadercommands/qb_cookie_source_test.go` and `internal/plugins/downloadercommands/testdata/source-qb-cookie01.json`. `overlay-checkpoint02.json` maps these and the four fixed checkpoint02 leaf files without modifying any repository file. `preparation01.json` binds exact target checkpoint02, baseline files, source raw/seal/tool receipt and all authored files. A future corrected target requires a new explicit input binding; old candidate evidence is not rewritten.

## Known static mismatch, not an executed failure

Checkpoint02 `qbCookieError()` returns generic `Invalid cookie header`. Actual original `cookie-invalid-name` emits ERROR `同步下载Cookie出错：Illegal key 'owned@bad'`. The test deliberately expects the original message and will expose the difference. Owner/coordinator are notified; this preparation does not adjudicate a new log-redaction policy, silently relax equality, claim the target passed, or call a merely static discrepancy an actual failure.

Remaining layers: native aggregate monotonic dispatch/authorization/unknown effects, vendor additional version gates/HTTP encoding, owned wire/SDK/App/ordinary CLI and real daemon behavior are separate. The 52 outer protocol calls are not 52 HTTP requests. Prior S03 34-case leaf/SDK/host/CLI credit is unchanged and not rerun here.
