# zhconv original fixed corpus01 — actual finite acceptance

**ACCEPT the retained original native-source observations. Not target-Go equivalence or recognition/host acceptance.** The reviewer only read existing evidence and checked hashes; no source/native rerun or new test.

## Actual execution and bindings

The coordinator transcription identifies tool chunk `03fc0a`, no session, exit0, tool wall time0.251472905s. Original stdout is2476503B, SHA **`fb4e540a618e575f7114d3e2a0055050e3fcfbf25a139323385351e40dabe3bb`**; original stderr173B and exitfile0 remain unchanged. The receipt is accurately labeled a coordinator transcription, not raw tool replay. Prepared COMMAND and actual executable identify the admitted isolated Python3.12.14 `-I -S -B` collector; no separate exact start/end timestamps are invented.

- Candidate02 collector `f66e55f6…`; manifest `1ae21e58…`. All **84** input paths independently rehashed now, exact size/SHA; actual collector before/after drift lists empty.
- All **4789** actual input objects exactly match the frozen case list in order. Every input UTF-8 SHA agrees.
- **4788 string returns +1 actual exception**. Every returned JSON string was independently re-encoded and checked against its retained UTF-8 byte length, strict base64 and SHA. Empty string and control/non-BMP strings remain literal data, not lost or normalized.
- Actual file-backed modules42 before/44 after all match bound current bytes. The only new file-backed modules are `zhconv_rs` initializer (`b0451dda…`) and its ABI3 extension (`c048aba5…`). Package-import3/init-exec1/read-only-open129/cache-refusal1 counters match the output/receipt. The single local cache-probe refusal is the admitted audit behavior, not a tool or OS permission denial and not a native syscall trace.

## Meaningful original behavior preserved

The actual call uses two arguments, `zhconv(text, 'zh-hans')`, default **wikitext=true**. Ordinary text, all4685 Hans keys,24 overlap inputs, and authored bounded syntax cases are original outputs—not values computed by the target.

Examples inspected directly include: inline `简體` is not re-converted; global replacement `頭髮` stays literal rather than becoming `头发`; matched script content remains unchanged while adjacent text converts; depth10/11 have distinct original outputs; removing builtin replacement `后` differs from naming source `後`. These facts are not generalized to unobserved cases.

The last string `-{=>zh-hans:甲;}-` actually raises **`pyo3_runtime.PanicException`**, message `assertion failed: 0 < i && i < s.len()`. Stderr points to `src/rule.rs:313:21`, matching the complete bound original assertion; the Python traceback points to the exact native call site. The collector catches and records this actual native exception, so process exit0 is corpus completion, **not 4789 successful conversions**. Stderr is retained, not scrubbed into a normal fallback.

## D description ordering — actual, not hypothetical

The same multi-variant D string was called four times in this one process. It produced two actual strings:

- `简体：甲；繁体：乙；大陆：丙；`
- `大陆：丙；简体：甲；繁体：乙；`

All raw strings/base64/SHA remain intact. Their three exact fragments and multiplicities agree; raw order does not. This aligns with original `VariantMap<String>` wrapping HashMap and `Display` directly iterating it (`variant.rs:89,230–235`). Only this original unordered variant-map description dimension can justify a separately explicit permutation comparison; **no sorting/normalization of ordinary text or the oracle is permitted**. For unidirectional D descriptions, original same-variant vector order remains meaningful (`variant.rs:240–247`); this run does not demonstrate every ordering or a probability distribution.

## One case-name caveat

The label `add-remove-add` contains the exact string junction `}-{-|`, not the valid adjacent-marker `}--{-|`. Actual output retains a remove-like literal substring. Preserve that exact same-input differential, but do **not** cite its label as empirical coverage of a well-formed add→remove→add sequence. No input correction or repeat run was requested/performed; independent static source semantics remain separate.

## Scope

This accepts a fixed original dependency corpus, including actual exceptions and nondeterministic ordering. It does not establish a new Go implementation, full wikitext equivalence, complete name search, another public variant/API, network/provider/report/SDK/host behavior, or native-syscall isolation. Original candidate01's pre-execution static HOLD and candidate02 correction remain retained. Precise hashes, all result checks, selected untouched observations and the exception are in `actual-readback.json`.
