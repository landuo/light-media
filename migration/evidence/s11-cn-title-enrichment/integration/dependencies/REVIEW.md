# CN provider correction: minimal db671 release dependency review

**Conclusion: the CN backend correction is independently applicable to `db671b145270084bc769e433cd7c68952cb8c6d0`; it does not require the uncommitted Meta protocol/cache/report implementation. This is a read-only dependency conclusion, not a clean-baseline runtime result.** No checkout, worktree, product edit, build, source import or network operation was performed.

## Exact baseline and necessary product bytes

`core_business_source_facts.go` is byte-identical between HEAD and ACTIVE: SHA256 `664ed8e6fbdbf097e4d885b083b8189c176d67a2d8be7215e67c45fbebd8704c`. Candidate02's existing two-hunk patch therefore applies to this exact HEAD preimage without relying on any Meta WIP. Final candidate source is `1c2e1777548211dfc55881ce11988f520573ff562207053bb9b4020dccf4437e`.

The phrase “four product files” is accurate **only for Go files**:
1. existing `internal/app/core_business_source_facts.go` — import + CN predicate/definite numeric TypeError branch;
2. new `internal/zhconv/hans.go`;
3. new `internal/zhconv/rules.go`;
4. new `internal/zhconv/wikitext.go`.

A fifth **runtime source input** is mandatory: `internal/zhconv/data/zh_to_hans.json` (4,685 original pairs, SHA `dfda8298299cdd7c6ddb19d0c76a1819605f9a6640523b7412bdcb92c1eb98f4`). Preserve data provenance and all MIT/Apache/GPL/data notices with the component; do not describe the MediaWiki table as MIT-only. Copying the standalone draft's `go.mod` would be wrong: production belongs to the existing light-media module, uses only stdlib, and requires no dependency/version change. Exact tests/oracles and necessary review evidence are additional validation files, not hidden production dependencies.

`originalMediaRecognitionSourceError`, the direct-only original host catch, noMeta kwargs normalization, explicit-ID native selector, guarded constructor/session/request cache, model/category projection, SDK request type and resource capability are already delivered in db671. No new source-error type, SDK method, native provider factory, runtime field or resource hook is needed for CN.

## Delivered helpers versus the mixed current build

The machine readback compares 24 relevant paths against their actual Git blobs: **21 identical**; three are known Meta WIP:

| Area | db671/current fact |
|---|---|
| Existing owned App install/provider/cold harness, fixture main/test | Present at db671, current bytes identical |
| `originalMarketOwnedIndex`, registry result helper, statistics isolation, auth/callTest | Present at db671, current bytes identical |
| Native client/model/category/error/startup helpers, original SDK, catalog session/snapshot seam | Present at db671, current bytes identical |
| `plugin_shared_media_recognition.go` | Current adds Meta-state dispatch only; retain HEAD version for CN release |
| `plugin_original_modules.go` | Current adds mutable Meta declaration/dispatch/result handling; retain HEAD version |
| `runtime.go` | Current adds Meta identity cache/report fields and startup/Close handling; retain HEAD version |

The new CN test calls no new Meta symbol. `Options{DataDir:...}`, the fixed catalog constructor injection, install helper, worker snapshot type, native call and cold reinitialization all exist in db671. The test worker uses only the delivered noMeta `SharedMediaRecognition.RecognizeMedia`; no uncommitted `RecognizeMediaWithMeta` dependency exists.

However, the current union-overlay compilation includes ACTIVE's Meta WIP and its tests. Even if that gate passes, **its complete input closure is not db671+CN**. Do not transplant its program hash or call it an exact release-candidate gate. A later coordinator-controlled exact-baseline candidate must be compiled and run with its own source/tool/program bindings; preserving ACTIVE dirt is required. This report creates no new worktree or candidate tree.

## Keep the old fixture reproducible

Never overwrite the delivered `cmd/owned-media-recognition-fixture/main.go` (900045). The CN worker draft changes exactly three native-ID constants to900071; module_hit900043, module_false900044, manifest/API and concurrency are unchanged. The source CN inputs genuinely use900071, so changing expected oracle IDs instead would not be an exact same-input comparison.

Smallest reproducible option: retain the CN-specific **test fixture source text + three-ID patch + original-main SHA** in a test/evidence location, generate a standard one-file Go overlay into a controlled runtime directory, and compile the existing cmd with that overlay. Store the resulting binary/source-closure SHA and supply it only to the CN root via `OWNED_MEDIA_RECOGNITION_WORKER`. The old root still needs its original900045 worker; never run both incompatible roots under the one modified fixture ENV and claim general regression credit. A new independent fixture cmd could work but duplicates the entire worker and is unnecessary; no new implementation is proposed here.

The App CN test and raw embedded CN5 fixture may be committed normally once accepted. A clean baseline with the actual `internal/zhconv` directory no longer needs the product-directory overlay or its vet workaround; the worker-only overlay changes an existing path.

## Minimum release gate, not a historical rerun

After the exact baseline is selected by the coordinator, compile the affected App package and one fixed CN worker. Run one App test program with:
- `TestSourceTMDBProviderInfoCNSource05` — exact four source dicts + direct TypeError;
- `TestOriginalMediaRecognitionOwnedProviderCNSource05` — six SDK calls/five actual owned GETs/two artifact GETs, recovery and warm/cold;
- `TestPluginNoticeV2TMDBMediaInfoOriginalAST` — retained 80-case original model oracle, because the changed provider helper is also called by `sourceTMDBMediaInfo`;
- `TestOriginalRecognitionNativeModelAndGroupBoundaries` — the existing narrow category/source-error/model seam, not a new matrix.

The existing converter's exact-source normal/delta/race evidence remains component evidence if its Go/table bytes and dependencies stay identical. Bind/compile its relocated package; a bounded `TestHansConcurrentIsolation` race plus the actual owned App root race are the relevant integration additions if the coordinator requires race on the final candidate. Do not rerun all43, whole SDK, provider HTTP matrices or unrelated business suites. Normal/race binaries and the complete source closures must remain distinct in evidence.

The production direct consumers are the existing noMeta native wrapper and `sourceTMDBMediaInfo` (including `sourceRecognizeTMDBCore`). The 80-case model gate is the minimal direct regression coverage; numerous tests that only construct a fixture through the same helper do not each require a full business re-execution. No new UI, route, endpoint selection or business migration is part of this patch.

## Boundaries that remain unchanged

- Full source get_info dict comparison is exact except its named MediaType enum representation. No independent new full-MediaInfo oracle exists for these CN fixtures; complete key checks and warm/cold byte equality are not that oracle. Retained source-model/category credit stays separately identified.
- Original `ServerHelper._build_recognize_report_payload` returns None for absent meta. This noMeta test neither exercises nor replaces the blocked with-Meta report path, the Meta roundtrip, name recognition, ordinary-server production TMDB transport or UI/browser acceptance.
- Precision correction to the earlier test prose: the inherited `disableResetFixtureStatistics` helper **does** set `PluginStatisticShare=false` and `PluginServerURL=""` as its already-delivered center-isolation behavior. It does **not** change `MediaRecognitionReport.Share/API`, `ProxyHost` or proxy environment. NoMeta report absence follows the source branch and existing target route, not that statistics helper. Frozen source/test/old reports are not rewritten here.

Readback: `READBACK.json`; path-only dirty observation: `status-observed.txt`. Both record observations, not a permission to reset, delete or include unrelated WIP in a release.
