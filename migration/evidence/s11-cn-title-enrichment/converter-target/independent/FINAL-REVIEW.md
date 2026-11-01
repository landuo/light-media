# Isolated zh-hans/default-wikitext component — finite ACCEPT

## Decision

Accept checkpoint03 as the independently reviewed, isolated pure-Go component implementing the consumed original `zhconv(text, 'zh-hans')` two-argument/default-wikitext operation. No concrete correctness blocker remains in the inspected fixed-source algorithm/data or executed finite comparisons. This permits subsequent scoped name integration, not a claim that name/SDK/host/report integration has already passed. ACTIVE was not changed by this review.

## Source and representation

Independently read the bound original `converter.rs`, `rule.rs`, `variant.rs`, `pagerules.rs` and `build.rs`, rather than inferring behavior from tests alone. The Go implementation preserves global pre-scan before visible rendering; original flag/invalid-rule fallback behavior; Unicode trim and ASCII variant parsing; Hans-specific fallback/no bidirectional global pairs; nesting depth 10 and HTML exclusions; entity-aware separators; accumulated removes (later adds do not undo them); replacement-text builtin removal; leftmost-longest secondary/builtin precedence; and non-cascading replacements. The different immutable rune-trie representation follows the inspected original matching choice. Per-call rule maps do not escape; shared base construction uses sync.Once. No runtime native/HTTP/other converter is used.

All 4,685 table pairs were independently reconstructed from bound original ZH_TO_HANS text with the original stable UTF-8 length/lexical sort and first-source dedup policy, then compared against the complete embedded table. Original GPL table notices and MIT/Apache algorithm notices are retained; not MIT-only. Fourteen checkpoint02 files remain exact in current checkpoint03 and the saved archive; checkpoint03 adds only the delta test and unmodified original delta raw.

## Actual evidence

| Gate | Actual roots | Recorded input count | Program SHA prefix |
|---|---:|---:|---|
| normal02 | 3 PASS | 1429 | f19f68958b24 |
| delta03 | 1 PASS | 1431 | 6e28b65ad02b |
| race01 | 4 PASS | 1468 | 8b8b43b5bb82 |

Original command exits/logs, exact selection, current programs, all recorded inputs and each retained generated testmain were checked. No skips or race warning; race list/build both include `-race`. Normal02 has only before-compile and terminal-after-test maps, plus later current readback; **not** an after-compile third snapshot. Delta/race each have three equal run-time maps and matching current readback. Newly added delta test was not in historical normal02: old bytes are inherited accurately and the latest race program executes all four roots. Concurrent-isolation root exercises shared read-only state/per-call rules after prior roots initialize the table; no concurrent-first-initialization runtime claim.

Normal01 failed before compile because Go's automatic VCS status returned 128; zero tests ran. Its stderr/exit remain. Normal02 used `-buildvcs=false`, with no source alteration or Git diagnostic probing. Tool IDs are retained in an expressly transcribed receipt, not misrepresented as a new raw-tool replay.

## Comparisons and non-equivalences

- 4,783 ordinary returns are exact original byte strings, with source raw hash/UTF-8/base64 validated. One original malformed empty-from case produces a typed Go `SourcePanicError` with the observed `PanicException` class and exact assertion message. Parse/global/outer converter paths propagate this error rather than applying invalid-rule fallback. Original native 173-byte panic stderr and the collector's caught native exception remain intact. **Go does not reproduce Python/Rust stderr, panic mechanism or already-proven full-host failure behavior.** The eventual name/host adapter must preserve the typed failure; this is a separate integration gate.
- Exactly five D descriptions use bounded whole-clause permutations (at most 3 original clauses / 6 permutations), retaining labels, text, separators and multiplicity. The original HashMap stores variant groups without ordering, and original repeated identical inputs actually yielded different orders. Unidirectional same-variant vector pair order is meaningful and is not relaxed. Static source therefore supports this precise comparison dimension, not sorting every description, ignoring punctuation, accepting arbitrary output, or claiming Rust/Go random-distribution equivalence. Normal and race logs preserve all five raw source/target strings: four are byte-unequal in each run.
- The independent five-string source delta is copied verbatim, and all five target results are byte-exact in normal and race. Correct adjacent add/remove chains, long replacement-vs-source removal and secondary overlap are now actual witnesses. The old malformed adjacency case is unchanged, not retroactively relabeled as coverage.
- Go invalid UTF-8 rejection is explicit, not an equivalence claim for arbitrary Python objects/surrogates. No public extra dictionary/variant, network/daemon, source execution, full media/name recognition or pending report fixture acceptance.

This reviewer performed static/data/hash/log analysis only: no source/native/Go rerun or product edits.
