# S12 material release runtime — independent acceptance

2026-10-31. **Accept the executed finite material runtime boundary on `release-light-media`, based on `760720ed0a96fd9e7a647914c14545bde5ef857e`.** No additional concrete runtime blocker was found in the reviewed scope. This is not a full-S12, browser, full-source-HTTP or publication-success declaration.

## Exact candidate and actual gates

All 25 assembly paths match both the frozen whitelist and the independently reviewed development candidate: 24 S12 source/test/oracle paths plus the AGENTS protection update. Git status matches that list; the index was unchanged at review. New S06 history SDK/snapshot files and the development ledger are absent. Existing history/OCR/parser/cache dependencies match their committed base blobs.

| Actual gate | Result | Bound declared inputs |
|---|---|---:|
| Release normal and denied worker build/manifest | PASS | 1083 |
| Release App normal | All four roots PASS | 6189 |
| Release App race | Both roots PASS; no race warning | 6198 |
| Release ordinary server build | PASS | 3960 |
| Retained S10 shared-scope compatibility | One root PASS | 6195 |

The recorded phase maps, actual declared-file sets, current hashes, generated-source copies and program hashes were independently checked. All recorded phase/current comparisons have zero drift. These are declared compile/runtime input bindings, not exhaustive environment/syscall traces. Race instrumentation covers the App binary; S12 workers are normal builds. The S10 compatibility gate uses the current release App program and the exact previously accepted S10 worker, not a newly compiled worker or a new full S10 run.

## Ordinary CLI, not an in-process substitute

The exact reviewed driver `4108d175...` actually completed: 30 controller calls, 28 retained raw API bodies and 13 material queries. Each query was matched to its raw response, rather than accepting HTTP 200 or the summary alone. Normal hosts **23 → 58** each returned natural Wait 0; actual installed workers **41 → 72** were bound by parent PID, starttime and executable hash and observed to exit by the driver's held-identity checks.

Observed behavior includes None/magnet without material HTTP, typed JSON exception and same-worker recovery, descriptor query parameters and second-stage Cookie omission, real failure notification and recovery, full 16 MiB SDK reconstruction, two sequential full calls totaling **33,554,432 bytes**, and fresh-worker cold cache reads without artifact/source reacquisition. Both large results match independently reconstructed synthetic-byte hashes. The persisted notification retains its exact ID, timestamp and large-integer user ID after restart. The 36 counted callbacks are not physical wire-byte measurements.

The peer served only fixed owned routes; its one setup `/` response was separately recorded as completed 404. No forwarding or CONNECT occurred in this fixture. Controller requests were limited to the held owned `127.0.0.1:39123` listener. The actual driver retained its environment, reported no input drift, and had no forced-cleanup/error result. Resource samples passed at recorded steps; continuous or terminal reserve monitoring is not claimed.

## Boundaries retained

- Development App01/App02 failures remain immutable. Release App4/race2 are new successful executions, not relabelled old failures.
- Legacy serial caller cancellation follows the delivered bounded-drain contract, not immediate cancellation propagation. Site/proxy checks apply to acquisition; later immutable pages retain invocation authority, not invented per-page configuration revocation.
- The explicit 16 MiB budget remains a documented source difference, not a waiver or claim of unbounded Python parity. CLI peers are synthetic HTTP fixtures; source component observations and separate TLS transport evidence retain their own narrower levels.
- No real-site credentials, downloader submission, browse/RSS, browser/UI, arbitrary Python inputs, whole S12, remote commit/push or full-platform claim is made.

Exact evidence hashes are in `release-runtime-final-seal.json`. The reviewer only read/hashed files and wrote independent audit records; no product, ledger, stage, source execution, Go execution or network operation was performed.
