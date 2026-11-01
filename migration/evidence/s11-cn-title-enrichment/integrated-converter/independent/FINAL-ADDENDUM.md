# Exact materialization and integrated converter: independent final addendum

**ACCEPT for the 19 exact copies and the finite converter normal4/race4 integration.** Combine with `../final01/final-seal.json` for the clean-baseline CN App normal3/race3 evidence. No new App rerun or full S11/Meta acceptance is claimed.

## Physical bytes and timing

Coordinator materialization receipt records 06:12:53.729243–.771435 UTC and exactly 19 destinations: the approved 15 CN product/data/license/test files plus the two already verified converter tests and two original oracle JSON files. This follows the independent old-closure snapshot at 06:12:32. No old snapshot is re-described as unchanged after physical writes.

At 06:14:23 UTC, all 19 destination SHA256/lengths independently match their exact frozen sources. The sole existing destination, `core_business_source_facts.go`, is the authorized replacement of the base version. All other destinations were required absent by the frozen script before copying. All 25 prior protected dirty files still match their before-image SHA/length; ledger is intentionally excluded from that protection. The script has no deletion/staging/commit operation and does not rewrite the three Meta hooks. Their presence in the live tree therefore remains distinct from the clean compile view previously tested.

## Actual new-package gates

| Gate | Actual tool/session | Closure | Program SHA256 |
|---|---|---:|---|
| normal01 | 6d66cf → 39b381 / 89909 | 1448 inputs, 128 packages, 1428 declarations | `27337f01065526dd8e50eb0c13195eee1ab256a039df7351b5c9bd045f7b9040` |
| race01 | 6f6d44 → dbbcb8 / 75117 | 1485 inputs, 131 packages, 1465 declarations | `9587a8d3193bfdb7b0eea25fae369abb52bca3894e68b64faf20985dc36a9e9f` |

Each has before/after-compile/after-test identical maps and an independent current rehash with zero drift. Raw go-list declarations were independently reconstructed and agree; one generated test-main is preserved and hash-bound per gate. Both use the real on-disk `./internal/zhconv`, **empty overlay**, and both runtime-read JSON oracles are bound as additional inputs (not falsely attributed to Go source discovery). All 19 destination files are also bound, though unrelated App files are not compiled by this package gate.

Go-list, compile and test commands exited zero; stderr is empty. The exact four roots each ran and passed with no skip/fail:
- `TestHansOrderedSourceOracle`
- `TestHansBoundedDescriptionSourceComparison`
- `TestHansConcurrentIsolation`
- `TestHansSourceDelta03AdjacentAndRemoval`

Raw output confirms 4783 exact source returns and one typed source exception, with the five D-description cases separated; the five delta03 adjacent/removal outputs compare byte-exactly. D raw discrepancies remain in output: only complete-clause permutation is compared for the five bounded cases, not original order or randomized distribution. Malformed native input originally produced `pyo3_runtime.PanicException` and 173 bytes of Rust stderr; this Go gate verifies typed class/message/fatal propagation, not those stderr bytes or full-host failure equivalence. No original native execution was repeated.

The runner deliberately used `-vet=off`; **vet remains NOT_RUN**. Its inherited result suffix “virtual-only package directory” is not true of this now-materialized package. The coordinator clarification is bound and preserves raw results; there was no new vet failure or successful vet result. Neither this addendum nor the earlier clean overlay review substitutes for a future ordinary vet gate.

## Combined finite conclusion

The tested clean CN backend uses the same runtime bytes now materialized; the converter has additionally passed its already defined four roots at the final package path, in both normal and race builds. Earlier clean App/worker race and lifecycle/source comparisons remain valid historical evidence. This does not prove the live tree with unrelated Meta WIP is the release candidate, authorize those files into publication, repair the blocked with-Meta report gate, or claim a normal server/public TMDB/browser/UI/remote delivery.

Only readback/static checks and this audit were performed by the independent reviewer. Publication composition and commit remain a separate exact-whitelist check.
