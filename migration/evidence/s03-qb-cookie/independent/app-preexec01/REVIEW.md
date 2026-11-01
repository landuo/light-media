# S03 qB Cookie App/installed-worker gate — independent preexec

**READY**, only the frozen normal command in `app01/clean-view01/COMMAND.txt`: build the unchanged generic worker once, compile one App test binary, execute exactly `TestSharedQBCookieActualWorkerRecoveryAndCold`. No correctness or authority blocker found. This is static admission, not an executed result; no reviewer Go, original-source, service or HTTP execution occurred.

## Exact assembly and preservation

- Current HEAD is `60206809a9c46b9f11d8fb67590a889e24919df6`. Independently checked author candidate, clean seal and bindings; 237 hash-row checks / 123 unique paths, zero drift. The runner's 144 preflight rows comprise 3 initial + 113 additional + 28 replacement/preimage rows.
- All 28 mappings reconcile: 3 tracked Meta hooks equal actual HEAD Git blobs, 17 untracked Meta Go files map to empty strings only, 7 production mappings equal frozen candidate07 copies, and 1 new App root. The selected root exists exactly once in the effective App view.
- All 26 saved preimages match; all 25 non-ledger Meta files remain byte-identical on ACTIVE. Before/after/current Git status agrees. Mutable coordinator ledger is excluded from compile invariance; its saved preimage is retained, not restored. No physical replacement, hiding, reset, deletion or tree edit was performed.
- Seven delivered helper/worker/host files match HEAD blobs. Candidate07's prior finite leaf/native/vet credit is inherited, not reexecuted or expanded. Current App gate has no race or vet claim.
- Evidence, App binary and worker output paths were absent at readback. Runner03 refuses output reuse; validates virtual/backing presence and bytes; gathers App + worker go-list union; preserves generated inputs; checks before/after-compile/after-test input maps plus both binary digests; requires only the exact root PASS, no FAIL/SKIP. Actual closure, compiler results and runtime counts remain pending.

## Real path and bounded side effects

The new test installs the supplied normal worker artifact through the real registry and SQLite using ordinary App handlers. `originalMarketOwnedIndex` invokes the bound worker's standard `--manifest`, binds the payload digest, and the owned artifact peer serves only `/index` and `/worker`. Worker operations use the unchanged SDK; production `originalSharedDownloaderCommandResource` selects `newOriginalDownloaderCommandNative`, not a test factory. App APIs are real handlers invoked in-process, not a cmd/server listener.

Both new peers bind explicit `tcp4` / `127.0.0.1:0`, with failure terminal and no listener fallback. Fixed qB routes accept only synthetic inputs and return owned responses; the torrent URL is just a form string and is never fetched. There is no upstream, redirect, CONNECT, external daemon, proxy mutation, new path-root grant, source import or source/vendor execution. Existing statistics isolation deliberately disables sharing/clears its server setting in the owned DB; it is not a claim that every setting remains unchanged. No pending Meta reporting path is admitted.

## Assertions inspected (not observations yet)

- Ordered Cookie merge and SDK tuple / large string ID / SourceTruthy, one current invoke; old-version fallback keeps per-add Cookie and performs no store access.
- An actual saved successful callback frame is probed once on the same worker and must be rejected before qB dispatch. Warm identity includes PID, init count and invocation continuity.
- The owned setter commits synthetic state **before** returning its single 500; the operation must remain `host_operation_failed`, with no source tuple/False, no Add and no replay. This is deliberately different from the original recorder's pre-effect setter exception, and does not assert real daemon atomicity.
- A separate explicit recovery succeeds on the same worker. Close and same-DB reopen preserve configured downloader and installed generation/version, do not refetch artifacts or mutate qB automatically, and the explicit fresh Add uses a new worker PID.
- Planned totals are 5 current SDK invokes + 1 rejected stale probe, 34 qB requests (5 login / 17 version / 4 read / 4 set / 4 add), 1 selected failure and 2 artifact GETs. These are handwritten integration assertions, not original vendor wire observations and not actual PASS evidence.

Admission stops at this one normal App/installed-worker composition. It does not prove ordinary CLI, UI, NAS, real downloader behavior, full S03, full Meta, new race coverage or publication readiness. Preserve the first real failure and all evidence; any code/binding/HEAD drift invalidates this admission.
