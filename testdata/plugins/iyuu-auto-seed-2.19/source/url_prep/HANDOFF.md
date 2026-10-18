# URL preparation port handoff

Status: complete for the specified consumed closure; all build/oracle handles terminal. Sources and interface frozen.
This is a standalone nested module at `next/phase3/url_prep/`. No main-module,
nativecopy, phase2 or immutable directory was edited or integrated.

## API

```go
import urlprep "lightmedia/urlprep"
prepared, err := urlprep.Prepare(rawURL)
class := urlprep.ErrorClass(err) // "" on success
```

`go.mod` declares `module lightmedia/urlprep`, Go 1.23, no external dependencies.
The source path is requests 2.34.2 `PreparedRequest.prepare_url(str, params=None)`.
It preserves non-http colon scheme pass-through; this function is not a scheme
allow-list or network permission check. Actual consumed source exceptions are
`MissingSchema`, `InvalidURL`, and `UnicodeEncodeError`; a Go string containing
arbitrary malformed non-WTF8 bytes returns `UnicodeError` because no Python str
can represent it. Error messages are diagnostic, **class and output are the
compatibility contract**. `Error` is the exported concrete exception type.

## Product files

- `go.mod`
- `prepare.go`: requests flow, urllib3 URL patterns/normalization, requote.
- `idna.go`: complete consumed IDNA2008/UTS46 validation closure.
- `punycode.go`: strict CPython consumed encode/decode algorithms.
- `unicode.go`: exact CPython Unicode15 properties/lower/NFC; lazy compressed data.
- `internal/data/tables.z`: complete table data, SHA checked on load.
- Retain `LICENSE.requests`, `NOTICE.requests`, `LICENSE.urllib3`, `LICENSE.idna`,
  `LICENSE.PSF`, `LICENSE.Unicode` alongside derived source/data distribution.

`prepare_test.go`, `full_test.go`, `testdata/`, `reference/`, and
`cmd/resourceprobe/` are reproducible validation/evidence, not product runtime.
See `COVERAGE.md` for the full consumed closure and explicit unconsumed APIs.

## Source provenance

`reference/source-manifest.json` hashes the copied actual requests/urllib3/idna
source and metadata. `reference/cpython-source-manifest.json` pins public
CPython v3.12.13 normalization/type C sources. Python Punycode and urllib.parse
sources were read directly from the isolated exact image's standard library.
The generation scripts dump CPython Unicode15 normalization/properties and
IDNA3.18 Unicode17 status/classes/scripts/joining data, then emit a 167,359-byte
binary compressed to **70,745 bytes**. Full table SHA, versions and counts are
in `reference/tables-manifest.json`.

## Verification

- Normal full source comparison: **13,369,344 scalar-range records** plus
  **80,549 multi-character/length/context/Punycode records**, all matched.
- Unicode 15 NFC/lower: 5,570,560 scalar-range outcomes.
- IDNA3.18: 4,456,448 scalar-range outcomes.
- Actual requests PreparedRequest: 3,342,336 scalar-range outcomes.
- Additional 23,242 URL, 29,952 IDNA, 27,355 Unicode sequence cases include all
  canonical decompositions and all 11,172 Hangul syllable decompositions.
- Dedicated parallel reuse: 16 goroutines × 1,000 mixed IDNA/URL calls.
- Normal final: `go test -count=1 -v ./...`, handle 62060, exit0, 12.636s.
- Vet final: `go vet ./...`, handle 2cffd0, exit0.
- Race final: `go test -count=1 -race ./...`, handle54985, exit0,245.077s; full scalar and multi-character suite, no short skips.
- Linux CGO0/amd64 test build: handle 983474 exit0; same complete source corpus
  run in the isolated NAS image, exit0, scalar phase42.01s; all other tests PASS.

`reference/build-handles.json` and `reference/oracle-handles.json` retain all compiler/oracle handle terminal states.

All NAS experiments used the fixed public image
`sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881`,
network=none, UID65534, cap-dropALL, no-new-privileges, read-only root/config,
256MiB memory, 48pids, empty env via env-i, owned public-only read-only binds,
and a64MiB tmpfs. **Nine** owned UUID containers/stages all exited0 and have
verified absent container/stage checks in `reference/isolation-summary.json`.
No original container/config/environment/credentials/seed data was used.

## Resource measurement

Linux amd64 Go1.26.2, CGO0, same fixed isolated NAS image, 20,000 mixed URLs:

- Before first call RSS6,652KiB.
- Cold table initialization + Unicode URL:6.408ms.
- After GC RSS9,196KiB; heap allocation1,160,096 bytes.
- Final RSS11,172KiB; peak RSS11,940KiB (~11.66MiB).
- p50 24.861µs / p95 87.028µs / p99 213.206µs; total746.214ms.

This is a standalone microbenchmark, not a whole-App memory claim. Raw output,
binary hash and container terminal/cleanup are in the resources evidence files.

## Limits

No unclosed implementation branches are known within the specified str URL /
params=None closure. The finite differential corpus is evidence, not a claim of
exhaustively enumerating every possible multi-character string. Standalone
bytes/object coercion, params serialization, IDNA display/decode/transitional
APIs, allocation failures, adapters/redirect/network behavior are outside this
API and explicitly not claimed.
