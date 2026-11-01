# S11: finite CN title enrichment for explicit-ID recognition

**Finite local acceptance; commit, remote push and CI are separate receipts.** Base `db671b145270084bc769e433cd7c68952cb8c6d0`; fixed MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`. The db671+CN clean compilation-view normal/race each have **actual three-root PASS and non-author finite acceptance**. The subsequent nineteen exact source/support copies and on-disk converter normal4/race4 also have actual non-author acceptance. These are distinct from the preliminary mixed-WIP gate; the live tree still contains separately protected Meta WIP and is not itself claimed to be the release checkout. S11 and the fixed platform scope remain implementing.

## Changed behavior

The already delivered explicit-TMDB-ID/no-Meta host can use the original CN alias selection rule: preserve an already-Chinese display title; otherwise choose the first Chinese CN alias whose **zh-hans/default-wikitext conversion equals the original alias**. Do not simply convert a traditional alias and select it. If none qualifies, preserve the existing SG/original-title fallback. A definite numeric CN alias title produces the source TypeError classification; the existing direct host source-error catch returns None, rather than successful fabricated metadata.

The converter is pure Go. Production does not call Python, Rust/native.so, an online converter or another provider. Its 4,685-pair MediaWiki table is **GPL-2.0-or-later**; the translated algorithm's upstream notices are **MIT OR Apache-2.0**. Complete notices and table provenance are under `internal/zhconv/licenses` and `data/provenance.json`. The original data member SHA is `5cb0019b32bb39ec5c6e662029f90bd166f7a844efb3bc877f9be41fdd511bf2`; generated table SHA is `dfda8298299cdd7c6ddb19d0c76a1819605f9a6640523b7412bdcb92c1eb98f4`. This is not an MIT-only data package.

## Separately measured evidence

- **Original CN5:** complete fixed get_info/helper nodes, recording detail port and actual exact zhconv-rs0.4.2. Four full provider dictionaries returned; the fifth numeric-title input raised real TypeError after the recorded partial dictionary enrichment. Six actual two-argument conversions. Full raw SHA `285ad4a28c5b5bc03533baff22ff1bb43da490fa27be99222d3de1aa2f86dffa`, stored once in `internal/app/testdata/source-provider-cn05.json`. No real provider HTTP or complete MediaInfo was observed in that original collector.
- **Original converter corpus:** exactly **4,789** inputs, **4,788 returns + 1 PanicException**, plus a separately captured **five-input** adjacency/removal delta. Of the returns, 4,783 are exact byte comparisons; exactly five D descriptions allow bounded whole-clause permutations because the original HashMap order was observed to vary. Preserve multiplicity, labels, punctuation and meaningful within-clause order. All five new delta outputs compare byte-for-byte. The malformed old adjacency input/raw remains unchanged. A typed Go SourcePanicError preserves the observed class/message, not the Rust/Python panic mechanism or stderr. This is not “4,794 successful conversions” and not a random/all-input proof.
- **Accepted isolated converter:** actual normal02 three roots, delta03 one root, race01 four roots; independent finite final included. The original normal01 failed before compilation on VCS metadata lookup and remains preserved. The retained original normal02 has before-build and after-test maps, not an invented third after-compile map.
- **CN function preliminary:** one actual source5 root passed using the unchanged executable produced by the earlier `go test -c` attempt that exited1 at vet's virtual-directory chdir. The compile command is still FAIL; it is not rewritten as successful. Four provider dictionaries compare exactly apart from the named enum representation; the fifth requires direct TypeError.
- **CN owned preliminary:** one actual installed-worker App root passed. Six SDK calls, five exact owned provider GETs, two artifact GETs, same-worker source-error→recovery/cache hit and same-DB cold reopening with a new worker were measured. All five returned models' complete `tmdb_info` agree with the corresponding original provider dictionaries. This build included current Meta WIP and used `-vet=off`; it is not the clean db671+CN gate. The inherited statistics helper isolates old center reporting, but does not disable MediaRecognitionReport; noMeta is what excludes report invocation.
- **Clean db671+CN:** actual normal01 and race01 each pass CN source5, owned installed-worker CN and the existing model/group-boundary root. Normal App program `8acd74e2321a07f678aa355ede4ef8505f8e86c155259e5e5719d00923cb560b`,6352 declared inputs; race App program `c84577039beb608ad3fefa4f44c6be3edf1bbb45af0e95f9a13823bd627b5601`,6361. Both explicitly leave vet NOT_RUN. The non-author final closure review accepts this finite clean candidate; its current-input check was at06:12:32, before later physical materialization. Do not describe the old whole physical closure as unchanged after that intentional write. No ordinary-server/UI or unselected old80-case root rerun is inferred.

- **Materialized converter:** actual nineteen same-byte files copied at06:12:53; all twenty-five protected prior source files remained identical. The on-disk `internal/zhconv` package has empty overlay and actual normal4/race4 PASS, independently accepted. Normal program `27337f01065526dd8e50eb0c13195eee1ab256a039df7351b5c9bd045f7b9040` /1448 inputs; race `9587a8d3193bfdb7b0eea25fae369abb52bca3894e68b64faf20985dc36a9e9f` /1485. This is package relocation/integration evidence, not another App run. Both use `-vet=off`: vet remains NOT_RUN. The raw runner's “virtual-only package directory” explanation is inherited text, not true of this newly materialized package; its correction is separately retained without changing raw results.

The real target chain is App.New/HTTPHandler→installed concurrent worker→ordinary SDK→current guarded native/cache/model→constructor-bound owned HTTP. The trusted test transport checks fixed logical provider GETs and constructs only the captured owned request; it never connects to production TMDB. No CONNECT/upstream forwarding, fallback route, environment proxy/CA change, real media/download or credentials are used. This is **not** cmd/server production-native HTTP acceptance or a browser/UI gate.

## What is inherited and what remains open

The already delivered explicit-ID SDK, native selection, complete-model/category machinery and their source evidence remain separately credited in `../s11-explicit-tmdb-id/`. They are not a new complete-MediaInfo oracle for these exact CN inputs. Same-worker/cold equal model bytes prove self-consistency, not original-source equality for every model field. This patch adds no new capability, settings/API/UI entry, Meta mutation/cache/report lifecycle, name search, provider family or runtime dependency.

The default delivered worker remains unchanged. The CN-specific test worker source text (`internal/app/testdata/owned-media-recognition-cn-main.go.txt`) changes only three owned native ID constants; compile it through the recorded one-file overlay and use its measured binary only with the CN root. Do not run unrelated old900045 roots with that900071 fixture and claim regression coverage. Converter tests/original raws are included under `internal/zhconv`; no standalone go.mod is added.

## Retention and publication safety

Each full original raw appears once in product testdata. Source collectors, literal inputs, node/manifests, actual stdout/exit/result/tool receipts and non-author finals remain in this evidence package. Repeated full Go dependency maps and larger readbacks are exact-SHA references with offhost recovery locations, not absent/unverifiable claims; public runtime outputs still support every listed finite observation.

The root-confirmed0603 backup is `libfile_8d53a205cebc81918bfc9f59f4723f95` v0, archive SHA `9e6b8174bd7214e14ab347e1cbb156c42b9d43ca51c2051e658d971ec3bca715`, with its0552/0517/0506/0456/0433/0443 prerequisite chain. It preserves original histories, including failures and unrelated WIP, and is **not** a Git delivery receipt. The later clean and materialized gates are actually retained in0616: `libfile_10609585da94819182f7ccfb7f739e4b` v0,4,970,366 bytes /184 tar members (183 payload rows plus MANIFEST.json), archive SHA `61439f966b35420d7520590507d4a551583a03977cbf1a1444f3a408b8050e61`. Its root-confirmed receipt and exact member hashes are bound; all80 external maps/readbacks have a matching member in0616 or the prior chain. This is offhost retention, not Git delivery. Do not copy the whole backups/audit tree into Git.

No databases, compiled binaries/native extension, dependency trees, auth state, login bodies, full private host logs, credentials or unrelated Meta/name/Search work belong in this publication. Commit, remote readback and CI are separate facts and remain unclaimed here.

## Minimal reproduction from the published clean CN commit

Use an already installed pinned Go toolchain and cached dependencies, in its existing local/offline build profile; leave proxy/CA/hosts and all production settings unchanged. Check out the eventual exact CN publication commit cleanly, **not the live mixed Meta WIP tree**. The commands below are a reference equivalent to the recorded build/test phases, not a newly executed script. They create only an owned temporary output directory and retain its files. No new provider endpoint or authority is granted.

```sh
# Preconditions: current directory is the exact clean CN commit.
# PINNED_GO and OWNED_OUTPUT_PARENT are caller-selected existing local paths.
RUN="$(mktemp -d "$OWNED_OUTPUT_PARENT/s11-cn.XXXXXXXX")"
python3 - "$PWD" "$RUN" <<'PYCODE'
import json, pathlib, sys
repo, out = map(pathlib.Path, sys.argv[1:])
(out / "worker-overlay.json").write_text(json.dumps({"Replace": {
    str(repo / "cmd/owned-media-recognition-fixture/main.go"):
    str(repo / "internal/app/testdata/owned-media-recognition-cn-main.go.txt")
}}) + "\n")
PYCODE
"$PINNED_GO" build -overlay="$RUN/worker-overlay.json" -mod=readonly \
  -buildvcs=false -p=1 -o "$RUN/owned-worker" ./cmd/owned-media-recognition-fixture
"$PINNED_GO" test -mod=readonly -buildvcs=false -vet=off -p=1 \
  -c -o "$RUN/app.test" ./internal/app
(cd internal/app && OWNED_MEDIA_RECOGNITION_WORKER="$RUN/owned-worker" \
  "$PINNED_GO" tool test2json -t -p ./internal/app "$RUN/app.test" -test.v \
  -test.run '^(TestSourceTMDBProviderInfoCNSource05|TestOriginalMediaRecognitionOwnedProviderCNSource05|TestOriginalRecognitionNativeModelAndGroupBoundaries)$' \
  -test.count=1 -test.timeout=120s)
# For the corresponding race build, add -race to BOTH worker and App builds.
# Preserve RUN; do not delete unknown external files or reuse the wrong worker.
```

The one-file overlay applies only to worker construction; the App build uses the published on-disk code. The only per-child environment input introduced is the necessary fixture binary path; no proxy/CA/environment clearing occurs. The external `reproduce-cn-static.py` draft adds precondition/hash/output checks but is **AST-checked only, never executed and not a delivery or acceptance dependency**. Actual gate receipts, not this illustrative helper, establish the results above.
