# TR final two methods — actual normal and race component review

**ACCEPT the executed finite component gates.** This is not installed-worker, normal-server CLI, real-daemon or full-S13 acceptance. No tests or source were rerun by this reviewer.

## Actual runs and bindings

Normal outer session37908 (`c7375a`, intermediate `7a81bb`, terminal `11a28b`) and race outer session99366 (`871971`, intermediate `ac850e`, terminal `507f04`) both ended exit0. Per-gate command/exits, raw test2json streams, result receipts and coordinator tool transcriptions were independently read; all selected roots ran and passed, with no skip/fail or DATA RACE warning.

| Layer | Roots in each mode | Normal inputs | Race inputs |
|---|---:|---:|---:|
| Leaf/source comparison | 2 | 1775 | 1812 |
| Ordinary SDK wire | 2 | 1935 | 1972 |
| Native new3 + affected oldTR3 | 6 | 2620 | 2628 |
| App component | 2 | 6243 | 6252 |

Each gate's before/after-compile/after-test input maps are identical. Every declared current path/size/SHA was independently checked without drift, as were all eight actual programs and retained generated-testmain copies. The eleven integrated paths match integration01 and their frozen candidates. Matching normal/race **repository** maps are identical per layer (67/193/267/2899 paths respectively); race toolchain/generated closure differences remain separately bound, not falsely called identical whole closures. New unrelated worker-command files do not retroactively enter these four package maps.

Race `go list` and `test -c` both use `-race`. These roots execute component/HTTP/logger goroutines under the race detector; they are not an arbitrary concurrent-mutation stress claim. No worker was built or run by these gates. The inherited runner does not rewrite proxy/CA/guards; its explicit environment overrides are GOMAXPROCS=2 and CGO_ENABLED=1. The result's proxy-unchanged flag is not an independently captured full environment equality map. Resource evidence comprises three phase-entry free-space samples per gate, not continuous monitoring.

## What the passing assertions actually establish

**Source leaf.** The complete original 32,975-byte raw23 document is embedded, hash-checked and included in the compile input map. Every source case ran in both modes. The differential root compares full ordered traces, kwargs and Python type descriptors, bool outcomes, exact source exception class/message, argument retention and call counts. Actual aggregate remains9True/10False/4escapingValueError,12calls/6logs. The four outside-try conversions have zero call and zero caught-call log. It does not substitute target-generated expected source results.

Only unique string-label membership/cardinality uses the previously accepted projection on copies. The original raw order is untouched. Actual verbose output explicitly retains the difference: source `[owned-new, owned-retained, owned-old]`; normal target `[owned-retained, owned-new, owned-old]`; race target `[owned-old, owned-retained, owned-new]`. This is intentionally not claimed as identical wire sequence. All other arrays, trace order, types, messages and results remain exact.

**SDK.** Exact capability/required-null fields/context and one HostCaller call, strict bool/ValueError-only envelopes, duplicate/extra/ambiguous payload rejection, invalid input before dispatch, unchanged host errors, and no replay—including post-dispatch cancellation—passed using recording HostCaller, not an installed worker.

**Native.** Actual owned HTTP checks the legacy envelope, synthetic auth/session409→200, cached session, camelCase fields, false/zero and floating ratio tokens. Low RPC15 emits WARNING and still selects `torrent-set`. All-None fails locally with pinned no-args ValueError before HTTP and composes with the actual leaf to logged False; the recording-port True case is not used as native success. Owner/cancel/final-guard errors cannot become source False. A selected500 is observed once and remains unknown rather than replayed or source-caught. Old TR Start/Recheck/DeleteKeepFiles controls, Add and Stop selectors passed on the same current shared-session hook; this is the three named affected regressions, not the historical full suite.

**App.** The new tests distinguish supplied component ports from the actual fixed native factory talking to owned HTTP. The actual native path makes zero HTTP for an outside-try conversion error and produces the ValueError-only class envelope; labels and limits then perform exactly their expected session/set pairs. Call-scope/configuration guards, strict frame/128KiB admission, Close balance and wrapped/unknown/cancel preservation pass. The bounded `WARNING → WARN` App adapter is actually exercised with configured WARN threshold: the fixed low-version message is present through the existing log query with plugin/instance/call/phase attribution, while the native/leaf source warning spelling remains unchanged. This is real App log persistence in the component instance, not a public worker/CLI claim.

## Conclusion and evidence

No concrete blocker was found for the declared normal/race component layer. Existing strict TR TLS, trust_env=False, selected-effect protection and source/numeric domain boundaries remain intact. Required ordinary installed-worker/CLI integration is a separate next gate, not supplied by this acceptance.

Machine bindings:
* `normal01-actual-readback.json` — `f47164a83eb04d68714f887adf4cdf7c074dec30ed8b3411c60fad7343ff27a5`
* `race01-actual-readback.json` — `e9c9298a5ebd10fc6ad275f5570da15987b5372b7174f7dcbef9d6f75a18d3b0`

These retain all program hashes, raw-artifact hashes, exact root verdicts, source permutation output, App log projections, generated-copy bindings and execution boundaries. Earlier source/static reports remain unchanged.
