# S03 qB URL + Cookie — finite release material

Base: `60206809a9c46b9f11d8fb67590a889e24919df6`; source MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`. Exactly **7 product files**, 3 new tests, one unmodified source raw and five license notices. No SDK/capability, App production, worker, Meta/S14/Name or global transport change is part of this whitelist.

## Behavior and evidence

The existing AddTorrent URL + truthy Cookie branch now probes fixed PEP440 >= 2.11.3, parses/merges ordered source cookies, writes qB's configured cookie store and then omits the per-add Cookie. Definite source sync errors retain the original per-add Cookie. One native lock/scope/monotonic dispatch proof spans the entire operation: a possibly effected selected write never becomes ordinary source fallback, source False or an automatic replay. The tiny legacy comparator correction rejects the four non-ASCII aliases excluded by pinned packaging's ASCII grammar.

Actual accepted layers are original recording17/18 → leaf normal3 (including old34 and boundary roots) → owned native normal4/race1 → separate exact two-package vet → actual installed worker/App normal1. The ordinary server double-host CLI also returned PASS (5 current operations + 1 rejected old callback, 34 fixed qB requests + 2 artifact GETs, both natural Wait0); its nonauthor final review was pending at the original draft seal and has now independently ACCEPTED the exact execution (CLI final seal ef8bec995346aaef879a2a6f74eede8f2acfcc9bddb6e8cd0f0dd6d3bb3af2d5). `evidence-index.json` binds original receipts and independent reviews; it is not a replacement for the external execution archive or a new review.

The source set-exception case is explicitly pre-effect. Owned native/App/CLI selected setter500 intentionally leaves synthetic peer state changed, stops before Add, and recovers only through a new explicit same-worker operation. Cold restart preserves installation/configuration without an automatic mutation or artifact refetch. This is fixed owned protocol evidence, not real qB/download/site/daemon, vendor HTTP execution, full S03 or arbitrary Python/URL/Cookie/PEP440 equivalence. Array/trace order and null distinctions remain; source JSON object key order alone is normalized in the pure differential.

Illegal-key/attribute errors retain key-only source diagnostics. Unobserved original diagnostic branches containing Cookie values stay sanitized and are not claimed exactly reproduced. Existing qB-only TLS, proxy/redirect/credential guards and cancellation policies remain. No application-level selected retry is added; Go's proven-zero-write internal transport retry nuance prevents a blanket physical-single-attempt claim.

## Reproduce only this finite target slice

Use a clean baseline plus this exact whitelist, **not the mixed ACTIVE Meta working tree**. All test data below are repository-relative; no local source interpreter or external absolute raw file is required. Run under the existing approved toolchain/environment without rewriting proxy/CA variables. These are instructions, not new executions by the publication author:

```sh
go test -count=1 ./internal/plugins/downloadercommands -run '^(TestQBCookieOriginalSource17|TestDownloaderLeafSourceObservations|TestDownloaderLeafBoundaryErrors)$'
go test -count=1 ./internal/plugins/iyuuautoseed/native -run '^(TestSharedQBCookieScopeWireAndVersionGuards|TestSharedQBCookieScopeAggregateUnknownAndRecovery|TestSharedQBCookieScopeGuardsAndLifetime|TestSharedQBCookieVersionOriginal23)$'
go test -race -count=1 ./internal/plugins/iyuuautoseed/native -run '^TestSharedQBCookieScopeGuardsAndLifetime$'
go vet ./internal/plugins/downloadercommands ./internal/plugins/iyuuautoseed/native
# Choose a fresh owned build output path; the fixture itself is unchanged.
go build -o "$OWNED_FIXTURE_OUTPUT" ./cmd/owned-downloader-command-fixture
OWNED_DOWNLOADER_COMMAND_WORKER="$OWNED_FIXTURE_OUTPUT" go test -count=1 ./internal/app -run '^TestSharedQBCookieActualWorkerRecoveryAndCold$'
```

The native/App roots bind only explicit IPv4 owned listeners, no fallback or forwarding. The App helper deliberately disables fixture statistics and empties its plugin-center URL; that is owned setup isolation, not a claim all settings remained default. No real downloader or backup is used. Selected ordinary CLI driver, non-login response bodies, owned host logs, execution receipts and independent reviews are published below. Large complete input maps remain in the exact externally retained archives indexed here; historical absolute paths are provenance, not a new execution. No private DB, secret key, login response or binary is included.

`qb_cookie_source_test.go` embeds `testdata/source-qb-cookie01.json` (exact original raw, copied once). Native original23 uses `../reference/native-projection-oracle.json`, already tracked in the baseline and SHA-checked by the test. Old34 similarly retains the baseline `testdata/source-leaves.json`. `provenance.json` lists their hashes and the unchanged helper/worker dependencies. Source bindings are immutable historical provenance, including the original legacy helper preimage; published product hashes are in the whitelist, not substituted into that old record.

## Notices

The bounded SimpleCookie translation retains CPython PSF and the original Timothy O'Malley notice. Fixed version grammar/comparison derives from packaging26.3; its dual BSD/Apache notices are preserved. See `licenses/` and `source-bindings.json`. These text attributions do not introduce third-party runtime imports, downloads or dependencies.

Publication note: the exact tested independent source-oracle test is retained byte-for-byte even though gofmt -l lists that one test; the other nine selected Go files are formatter-clean. This is a documented style observation, not a failing runtime gate or a claim of all-files formatting compliance.
