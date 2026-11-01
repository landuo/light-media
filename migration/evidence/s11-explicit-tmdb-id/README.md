# S11: finite active recognition, explicit TMDB ID / no metadata object

Base `911846b9a0cfe61da96d948d6b170f90f098c214`; fixed original MoviePilot
`8a83efeacd3e26b82be495e17d50146e31c96a60`. This is one vertical slice of
`ChainBase.recognize_media`, not completion of S11 or the fixed platform scope.
S11 remains implementing. Historical category credits are inherited, not reset;
no browser/UI, real TMDB, real media, downloader or NAS acceptance is claimed.
Assembly is not commit, remote delivery, or CI acceptance.

## Shipped finite path and separately measured layers

The new fixed `media.recognize` SDK preserves absence versus explicit null for
the 11 source parameters and exact JSON numbers; one bounded callback, no retry.
The supported active host slice has `meta=share_meta=None`, explicit TMDB ID,
current installation/capability/scope/config guards, source plugin-first module
dispatch and complete raw MediaInfo/None results. Self-provider reentry requires
the actual negotiated concurrent worker; it does not silently skip itself or
replace an older serial worker. Unimplemented/authority/unknown/cancellation
boundaries are not fabricated successful None.

* **Original source observations:** 12 fixed ID-selector cases, 10 returns and
  2 uncaught RuntimeError, 13 recording reads. Known TV None does not fall back;
  unknown type reads TV then movie even when TV succeeds. These are complete
  selected original nodes with recording dependencies, not original full App or
  real provider HTTP. The unchanged full raw is present only once, in product
  `internal/app/testdata/recognition-id-source01.json`.
* **Original cache/request observations:** 10 fixed scenarios / 23 actions,
  15 returns + 8 uncaught exceptions, 19 recording GETs, plus context-state
  observations. Complete raw stdout is copied unchanged. Recording cache/key,
  session/read and calendar ports are not real cachetools/Redis/HTTP evidence.
* **Pure/recording/component gates:** SDK normal2/race2 and worker normal2/race2;
  earlier selector1 and detail-reader2 normal/race keep their finite historical
  credit. Current native catalog5 normal/race includes the two new roots and
  three affected existing credential/redirect/redaction roots. Per-file/closure
  binding and independent reviews explain inheritance; this is not a claim that
  every old root reran on the final whole-tree closure.
* **Current App normal6/race6:** actual App construction, auth/install API,
  installed concurrent Go worker, current public SDK, real active host/native
  reader/cache/full-model code and actual fixed owned provider HTTP. The
  controller is App.HTTPHandler in a test launcher, not a cmd/server process.
  Ten SDK calls / nine actual provider reads / two artifact GETs witness cache
  hits, false/null fresh replacement, cached source-error JSON, recovery, TV,
  unknown TV-before-movie order and same private DB cold reopening. Actual peer
  traces must match on error/None steps too; a blocked request cannot count as
  the intended provider error. The trusted `_test.go` transport first validates
  official logical requests, then constructs a new request only to its captured
  owned listener. That explicit test routing is not production upstream access.
* **Ordinary cmd/server plugin-priority chain:** Actual ordinary cmd/server CLI: 4 business calls, 8 typed attempts, 7 callbacks (6 successful envelopes + 1 host error), 6 successful typed results, two naturally exited hosts; independent terminal seal included.
  It uses the owned self-provider model/falsey branch, error/pre-cancel recovery
  and same-DB cold startup without artifact re-fetch. TMDB credentials are empty
  and no native operation is invoked. Its separate success cannot stand in for
  ordinary-production-server native TMDB HTTP/cache acceptance.

The SDK/worker/launcher/CLI author is not their semantic approver. The component
and actual CLI independent reviews are included; this index is composition and
input-binding evidence only. Full compile/input maps are retained externally to
the Git worktree and referenced by SHA/count; dependencies/binaries are not
copied into the publication. Space retention requires an actual separate receipt,
not an assertion inferred from local files.

## Cache, transport and remaining source boundaries

The new in-process response-snapshot cache is distinct from the original
metadata-to-identity persistent cache. Source default normal/big-memory profiles,
day key, copy isolation, fresh-read bypass with replacement, retention after
failed fresh and valid error-JSON caching are finite implemented/tested facts.
Meta=None always reports `recognize_cache_hit=false`, even when its HTTP snapshot
was cached. Source recording evidence does not establish complete cachetools
key-token/LRU/TTL/concurrency or cross-process behavior. Nondefault
META_CACHE_EXPIRE, recursive response-header rate-limit waits and uncovered
Python key/value domains remain explicit boundaries, not approved equivalence.

New recognition-only approved TLS behavior stays on its private cloned session;
the existing catalog/UI transport and process proxy/CA/hosts remain unchanged.
Only definite no-response failures permit one guarded same-route session rebuild;
unknown/policy/cancellation failures do not retry or downgrade to None. Owned TLS
and constructor route checks are not upstream/CONNECT acceptance.

Still outstanding: metadata/name recognition and its mutation roundtrip;
original identity cache and shared query/report/backfill/statistics branches;
Douban/Bangumi/AniList and other provider inputs; complete phrase-level CN alias
conversion and other explicitly unsupported model/group/key domains. Finite
explicit-ID support does not satisfy those missing parts or all original Python
objects. Existing bounded ID/request/body/timeout contracts are not enlarged.

## Preserved failures and safe publication

Two authoring incidents are retained, not rewritten as successful preparation:
SDK first normal/race both failed compilation on a test error DTO type; the first
owned App gate failed compilation on two test credential/config type assignments.
No tests ran in those failed gates; corrected targeted gates ran separately.
The App failure's outer tool receipt was lost at context truncation; its actual
compile exit/result/stderr remain. No outer terminal chunk is manufactured.

Historical prepared candidate notes remain explicitly historical. The final
source bytes, accepted inputs and actual results are separately bound. Public
text excludes DB/private state, executables/dependencies, login bodies/cookies,
credential stores, full private host logs and unrelated S06/S07/Browse work.
Only four exact CLI business response bodies may be copied. Raw source/patch
bytes, including meaningful whitespace, are never silently reformatted.
The official structure check must preserve the exact ordered baseline errors;
unchanged known errors do not mean the global checker is green.
