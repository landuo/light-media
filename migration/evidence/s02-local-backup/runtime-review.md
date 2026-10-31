# S02 local backup material — finite final runtime review

**ACCEPT the reviewed ordinary local-backup material and private cleanup slice.** No unresolved implementation blocker was found within this scope. This is not acceptance of whole S02/TorrentTransfer, real downloader/NAS operation, browser UI or unbounded source filesystem behavior.

## Actual evidence independently read

| Gate | Actual result | Bound inputs | Layer |
|---|---:|---:|---|
| pure02 | 2 roots PASS | 2,897 | Selected 9 historical source cases plus finite read/rebuild port boundaries |
| sdk02 | 2 roots PASS | 1,924 | SDK recording-host wire, pages and release |
| fs01 / fs-race01 | 2 / 1 roots PASS | 6,200 / 6,209 | Owned positive filesystem and same-hash contention; historical pre-cleanup closure |
| cleanup01 | 2 roots PASS | 6,202 | Original six clear traces, owned cleanup and optional setting cold persistence |
| worker01 | 4 roots PASS | 6,201 | Final-candidate cleanup 2 plus installed-worker 2 |
| worker-race01 | 2 roots PASS | 6,210 | Host race instrumentation; worker remains normal |
| fixture02 / server01 | normal build PASS | 1,084 / 3,965 | Exact programs used by final CLI |

For each cited runtime gate, original result/exit/raw-test output, recorded phase equality and retained generated-source copies were checked; current rehashes were performed at the timestamps in their independent readbacks. Later independent additions/holding do not retroactively invalidate the older B2/C2 history, nor make its old complete closure current. Final pure/SDK/App gates explicitly bind the narrowed candidate. Fifteen S02 source/test paths are separately bound in the seal; ledger and unrelated Browse WIP are not part of that source set.

## Normal CLI

The exact frozen driver `1bcbc250…` used server `b999c2d3…` and normal worker `604ccbe2…`. Original coordinator callback transcription records session 28159, terminal 8261ea, exit 0, and matches the frozen COMMAND; it is labeled a transcription, not a new reviewer run.

- Two normal hosts **25 → 51**, both natural Wait 0; installed workers **42 → 66**, with held-parent/executable identity checks and completed known-worker disappearance checks in the frozen driver.
- **14 control requests / 12 retained raw bodies / 5 material queries**. All raw hashes and business responses were independently compared. Rebuilt source bytes are **182 B**; the synthetic TR raw vector is **1,048,579 B**, read in two actual SDK page calls. Missing resume is a successful skipped-item response, not bytes or a transport failure. An empty downloader selector produces actual `host_operation_failed`; the second item succeeds within that same worker invocation, followed by cold success.
- One install and exactly two fixed peer GETs (index/worker), no extra peer request, forwarding or CONNECT. Environment comparison passed inside the actual driver; no cross-session full-environment equivalence is inferred.
- Four owned backup files keep bytes, size and exact nanosecond mtime across seven checkpoints and independent final re-read. Fresh rebuilt temp persists across Close/cold and is reused by hash; it is not deleted on page release. The CLI does **not** claim an aged-file deletion threshold test. Eighteen event/terminal reserve samples passed, not continuous monitoring.

## Boundaries and inherited INFO fix

The private no-follow/root/downloader authority checks and first-boundary-error handling were statically reviewed; no new permission/inode/symlink-escape probes were required or run. The **16 MiB material bound** is an explicit target resource limit, not a claim of unbounded source equivalence. Only `App.Dir/temp/plugin-torrent-backup` is age-cleaned after producers drain. The original clear proof uses complete source AST with recording paths/fixed clock; positive native cleanup is a separate Go observation.

The already accepted shared request-INFO ordering fix remains semantically inheritable: its dispatcher, source fixture, log implementation/query and module/site consumers are byte-identical; S02 adds a disjoint capability, optional omitted setting and private missing-sandbox no-op at Close. Original **normal/race each 1 root × 3 subcases plus two-host 4-query CLI** credit remains historical; there is no claim of a fresh INFO run or old whole-closure equality. No extra historical rerun is required solely because the App package gained S02 files. The original missing-INFO FAIL remains preserved. This is not new Browse native-host/profile or serial self-reentry acceptance.

No Go, source import, service, network, filesystem probe, product or ledger modification was performed by this reviewer.
