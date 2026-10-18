# Encoding detector handoff — frozen candidate

Owned directory: `next/encoding_detector/`. No files outside this directory were modified by this work. No formal registration, user data or production service was changed.

Module: `lightmedia/encodingdetector`, package `encodingdetector`, Go minimum 1.24.0. No external Go dependencies, CGO, Python process, HTTP request or native extension is used at runtime.

## API and caller rules

- `Detect([]byte) (encoding string, confidence float64, err error)` reproduces requests' consumed default chardet result. Empty encoding represents original `None`; requests then decodes UTF-8 with replacement.
- `DetectAll([]byte) ([]Result, error)` exposes full default pipeline ranking and metadata. Internal empty strings represent original nullable fields; this is not the original Python JSON protocol.
- `Lookup(name) (string,error)` is **chardet registry lookup**, including its broader aliases.
- `Decode(data, encoding, replace bool)` and `DecodeErrors(data, encoding, mode)` use **Python codec lookup**, not chardet lookup. `mode` is strict/replace/ignore.
- `ErrorClass(err)` returns the original class consumed by requests. `CodecError` includes Class/Encoding/Reason/Cause. requests.text falls back to UTF8 replacement only for LookupError/TypeError; UnicodeDecodeError/UnicodeError/ValueError propagate to the original helper's catch boundary. Strict JSON guessing swallows only UnicodeDecodeError.
- UTF7, Unicode escape and Punycode can produce isolated surrogate values. They are retained in Go strings as WTF8; reject them at the same original lxml boundary. Do not silently replace through JSON or ToValidUTF8.

Strict complex IDNA2003/Nameprep/ACE roundtrip is explicitly **unimplemented and unconsumed** by requests.text. It returns DependencyUnavailable, never a fabricated successful result. Nonempty IDNA replace/ignore reproduces the original UnicodeError rejection. Strict ASCII fast path is implemented. See `codec-matrix.json` for the actual 111 available Linux stdlib text modules (110 canonical names); Windows-only mbcs/oem are unavailable in the reference runtime. Arbitrary user registered codecs/error handlers, Python warning emission, exact exception messages/offsets and nondefault chardet/streaming APIs are outside this consumed component.

## Integration files

Production: `models.go`, `stages.go`, `codec.go`, `codec_extra.go`, `iso2022.go`, `go.mod`, `LICENSE.chardet`, `LICENSE.PSF`, `LICENSE.Unicode`, and the exact go:embed files listed in `verification.json` integration.assets. Copying the whole `reference/generated/` preserves all source evidence; runtime embeds only the declared asset patterns. The 352 models, IDF and confusion bytes remain byte-identical to installed chardet.

Tests: `oracle_test.go`, `provenance_test.go`, and all 364 files in `reference/source-manifest.json`. The public source/oracle scripts are offline evidence, not product runtime. `reference/run_generator.py` and `run_nas.py` are optional owned verification runners, not required in the shipped program.

Source manifest SHA256: `1658b23f7acd3682367123ed609242c43b556f111b2e4454f68b2cfbcdf1365c`.

Actual Linux CGO0 test binary SHA256: `7c94619aa5abbd67dc0b69c84f855021f8202302515a047e29670375e2b67485`.

## Verification

Final normal: 12 top tests, zero failures/skips, 2.983s. Race: the same 12 top tests, zero failures/skips/race reports, 31.993s. Vet exit0. Source comparisons cover 1,568 complete detection samples and 11,348,289 decode inputs, including complete GB18030 four-byte structures, EUC-KR composites, UTF7 triples, complete two-byte spaces, actual stdlib aliases/error classes, every Unicode name, long escapes and the 200000-byte detection cutoff.

NAS: exact frozen binary and manifest files were executed in independent containers, network none, read-only root, UID65534, drop ALL, no-new-privileges, cleared environment, GOMEMLIMIT64MiB, owned public/synthetic input mount only. All 12 tests passed under a 256MiB limit; cold detection passed under 128MiB. No OOM. Latest cold 1,526-sample profile: 0.476s, HeapAlloc32,639,712 bytes, VmHWM83,360 KiB, VmRSS66,780 KiB. Full suite profile peak101,688 KiB. These include the test framework and synthetic fixture, not a measurement of the full application.

Final NAS owner: `29fdb336-3536-4b66-9c06-014582f36b59`. All 17 owned source-oracle/NAS container labels and directories were independently checked absent in `reference/cleanup-verification.json`. Every process handle is terminal; see `reference/process-ledger.json`. Earlier failed exploratory runs and pre-optimization proof are not counted as final passes.

Primary source copies: installed chardet7.4.3 source/models/metadata; actual CPython3.12.13 stdlib source and generated codec transducers; pinned CPython v3.12.13 C algorithm source; Unicode15.0 NameAliases admitted through actual unicode_escape. Licenses are retained. `verification.json` is the current report; `verification-before-stdlib.json` preserves an earlier snapshot and must not be presented as the final state.
