# S13 four native controls — finite runtime acceptance

Independent reviewer: c_userdata_owner; native author: tls_ab_independent. 2026-10-31 UTC.

**ACCEPT normal 3 + race 3**, only the frozen four-method native boundary. Read original test2json, command exits and programs rather than relying only on result labels:

- `evidence/s13-four-native01`: 2,607 inputs before/after compile/after tests and current rehash identical; all three exact roots ran and passed, no skip/failure. Program `dc831fd628558f711a888b1274623be66f7829e1d5ec0d55a98520ed2b2c6482`.
- `evidence/s13-four-native-race01`: 2,615 race inputs, same three-stage/current equality; all three exact roots ran and passed, no skip/failure/race warning. Actual list/build used `-race`. Program `eebd17e46c49062fd19641278c4df529caf1f0035bad4ee928ecfc0f87ad43de`.
- Roots: `TestSharedDownloaderFourQBWireAndVersions`, `TestSharedDownloaderFourTRStopSelectors`, `TestSharedDownloaderFourGuardAndUnknown`. Each gate preserved its actual generated testmain bytes, independently matched to the original cache input.
- Both copied target files still equal candidate01. All seven previous native files remain exactly candidate-bound; no old-client/TLS/proxy change in this slice.

Observed scope: owned HTTP responders exercise qB tag/category/stop fields and version gates, TR stop selectors, guard/cancellation rejection and selected-response failure as unknown effect. These are not vendor execution, a real downloader, installed worker or ordinary CLI evidence. No application-level selected replay is implemented; do **not** generalize observed peer request counts to a universal single transport attempt (see prior `TRANSPORT-ZERO-WRITE-CLARIFICATION.md`). No new probes, tests or imports were executed by this reviewer.

Native phases ended at 23:36:24.132759Z / 23:36:29.950422Z. Native results alone did not establish outer session 96045 termination because its server build continued; the coordinator subsequently supplied terminal chunk `c2a86b` / exit 0, now separately recorded in tool-receipt.json. This report does not count that server build as CLI execution.

Machine readback: `native01-runtime-readback.json`, SHA256 `b7f6029f1a5acdb9cbef8b8c47627102f2dba34c5bb973024488137b8d068f9c`.
