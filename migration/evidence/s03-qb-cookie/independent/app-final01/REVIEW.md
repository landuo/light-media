# S03 qB Cookie — finite actual App/installed-worker final

**ACCEPT: one normal App root actually PASS.** Exact preexec seal `5b8ee30c…` and frozen candidate07/clean-view01 were used. No reviewer rerun, Go invocation, source execution, service launch or HTTP request.

## Actual terminal and input closure

- Coordinator execution: session 11406, chunks `8e5f08` → `3825b2` → `64906b`, exit 0; runner 2026-11-01 07:38:01.066298 → 07:38:32.340621 UTC. Receipt binds unchanged result SHA `24b836328ae5aa9c5396b886b9fb82fb195f5c22fdaa6cf2881f60429f642e22`; the receipt is coordinator transcription, not raw tool replay.
- Exactly `TestSharedQBCookieActualWorkerRecoveryAndCold` ran and passed, with no subroots, FAIL or SKIP. All four recorded phases (go-list, worker build, App compile, test) exited 0 and have empty stderr. Test elapsed 0.34 seconds; this is not a race or CLI run.
- All 6,378 input rows match before / after-compile / after-test maps and their current bytes. Independently reconstructed raw go-list's 430 package records / 7,814 declarations / 6,274 unique effective file paths; declaration rows match exactly. Declared + explicit bound + overlay input union equals the frozen map with no missing or extra paths. One generated testmain copy matches its original frozen bytes.
- All 28 mappings remain exact. Current HEAD is 60206809; all 25 Meta files match their protected preimages, and before/after/current Git status agrees. Mutable coordinator ledger is intentionally not held constant. No dirty Meta content was substituted into the clean test view or removed on disk.

## Unchanged normal worker, eligible for byte-identical reuse

`runtime/s03-qb-cookie-app01.worker`: 5,358,855 bytes, SHA **75b1f85df152f0cb5dc9b0da6850ebd76632aa175e72d25aa486ab3457cb16e7**.

The actual single `go build` command targets ordinary `./cmd/owned-downloader-command-fixture`, and runner assigns this artifact to `OWNED_DOWNLOADER_COMMAND_WORKER`. Source main.go remains the delivered byte-identical fixture. The normal worker imports SDK plugins, not App/native test packages. Its 148-package/dependency declared union (1,776 paths, conservatively including available test declarations from go-list) is separately preserved in `worker-declared-inputs.json`; every row is within the checked 6,378-input union. The worker digest agrees with the runner's build/after-test digest and current bytes.

App test program: 93,039,958 bytes, SHA **d6250d516c2f6999cb79483b3026d8fc6d078d54cb1ecd1cd6a9711c5682d1c9**. The manifest/payload/installed worker path is the previously inspected normal helper and SDK path, not a fake host factory. The same worker binary can be reused under its SHA; any forthcoming ordinary CLI gate still requires its own admission and actual evidence.

## Actual finite behavior established

Passing immutable assertions and terminal output establish the following (these are fixture-level observations, not a packet capture or original-vendor HTTP oracle):

1. Real installed worker → SDK → App resource → production native qB path succeeds with ordered Cookie merge, large ID preservation and source tuple truthiness. Old-version fallback retains per-add Cookie without store read/write. One captured prior callback is rejected before additional qB dispatch.
2. The owned setter commits its synthetic Cookie state then returns the selected HTTP 500. App returns `host_operation_failed`, not a fabricated source False/tuple; no Add or replay occurs. Its changed owned state remains visible. A separate explicit operation recovers successfully on the same worker.
3. Close/reopen the same SQLite DataDir retains downloader settings and installed generation/version. At the checked lifecycle boundaries there is no native mutation or artifact refetch; the next explicit Add succeeds from a fresh worker. PID 145 → 153, invocation/init assertions pass.
4. Actual total: 5 current SDK invokes + 1 rejected prior probe; qB requests 34 = login 5 / version 17 / read 4 / set 4 / add 4; selected failure 1; artifact GETs 2. These counts are asserted by the frozen root and printed in its terminal summary.

The source recorder's setter exception was pre-effect. This owned partial-effect-then-500 observation is deliberately separate and does not claim daemon atomicity. Expected ERROR/http_500 is the selected scenario, not a failed test. Existing command-menu provider warnings do not establish UI/menu delivery.

## Limits retained

This accepts finite normal App/installed-worker composition only: in-process ordinary App handlers, real worker process and fixed owned tcp4 peers. No ordinary cmd/server listener/CLI, real qB daemon, external torrent fetch, user data, UI/NAS, Meta report, broad S03 or publication acceptance is implied. No new race or vet ran. `vet-scope-clarification.json` correctly preserves the raw runner's stale S11 explanatory wording while clarifying the actual `-vet=off`; previous two-package S03 vet remains separate credit. No evidence was rewritten or failure history removed.
