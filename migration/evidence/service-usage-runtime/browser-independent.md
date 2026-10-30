# Current 65eb service/statistics UI — independent evidence review

Status: **PASS — finite functional UI, static deliverable asset closure, and inherited backend gate; explicit native-runtime observation limit remains.** Cleanup is now cold-read confirmed. No application, test binary, Go command, listener, browser, or network request was run by this reviewer. Only existing files, source, hashes, and tool-result receipts were inspected.

## Fixed scope and inherited evidence

- Backend source `8a83efeacd3e26b82be495e17d50146e31c96a60`; frontend source `2829e3b08214348f05ff2f5beae644882eccad58`.
- Joint candidate `evidence/coordinator/platform-runtime-01/candidate.json`, SHA `bd30bc25e3fb5b98a9c670ce121c46d3b6b7770152fc9dcfb1a2423a16279a87`; 82 candidate files, 111 newly emitted assets, and 358 actually embedded asset paths currently match recorded hashes. These counts overlap and are not a behavior-coverage measure.
- Frozen normal server SHA `65eb917a297d81e96e3e160f47ae6a025ba03501fd01178ef0c2493f3bdde61b`; current launch file SHA `8fdcaeec9d5aa0cf63f541070a5a3148cb20cbd2dfb4a71488304b6375ad39d9`.
- Existing normal/race logs each contain 100 successful exact roots, no root failures/skips/race warning; group log hashes still match the recorded results. These are **inherited executed results**, not new runs by this reviewer.
- Prior independent backend audit `audits/recovery-storage-independent/integration-current-runtime/final.json`, SHA `10312ac3b5687e63b6b82a2ee9dd1aaa59c391071de06d9000adddcf58c42b6d`, retains its finite normal/race gate. This review does not reset historical completion or redo its 8 + 8 selected roots.
- Candidate explicitly excludes clone05c and the complete newer trigger-family delta. Neither those integrations nor whole-platform acceptance are implied by this UI report.

## Directly observed UI/peer results

Paths below are relative to `evidence/coordinator/platform-runtime-01/`.

### Service dialog

- `browser/step85-*`: normal waiting order is system, z-fast, a-slow, held with increasing next times.
- `step87-*` and `step90-*`: held appears first, reads **正在运行**, has an empty next-time cell, and the sampled execute button is disabled. Reentry receipts precede step90; no second gate arrival is present.
- `service-ui-peer/before-release.json` and `requests.jsonl`: exactly one controlled gate arrival, then one release/close. `step91-*` directly shows held back in waiting order with its next time and an enabled execute button. This is stronger than interpreting a 200 as business success.
- Error/list-fail exercises: `step128-*` directly identifies selected `error`, and later `step149-*` identifies `list-fail`; `step126-*`, `step127-*`, `step133-*`, and `step134-*` show waiting rows, not numeric progress. They do **not** show progress 37/100, error payloads, or the inner callback's completed result. Existing bound backend tests establish those internals separately. `executed-action-transcription.json` supplies the coordinator's verbatim transcription of actually executed click inputs, hash-bound to the original returned results. In 126/133, text is read **before** clicking, so these immediate waiting outputs are not even post-click outcome samples. Later 127/134 retain waiting. The transcription is action provenance, not a newly produced automatic network trace; filenames alone were not treated as click proof.
- `step96-*` is an actual selector timeout and is not a passed exercise. Later successful work must be credited separately, not relabel this old failure.

### Scheduler dashboard

- `step136-*`: no selected content; `step138-*`: toggle's sampled `aria-pressed` is false before enabling.
- `step139-*`: visible scheduler card, grid width **4**, actual auto-sized grid height **23**, no run buttons. `step140-*`: every listed next-time label drops by one minute, still no run buttons.
- Recorded file timestamps for 139/140 are 62.349086873 seconds apart. This is a file-capture interval, **not** a precise browser timer sample. Together with the executed-action journal it can support an observed autonomous display update over that interval. The 60,000 ms cadence comes from bound source/previous frontend timer tests, not a newly measured exact 60-second browser assertion.
- Source declares default `rows: 11`; existing GridStack content sizing can change the live height. Do not claim the observed height was 11. `step142-*` directly shows scheduler content surviving the official cold restart with the same data directory.

### Statistics, install reporting, and cold startup

- `step114-*`: visible order Higher **2.4k**, Standard **1.2k**, Base **7**. `step116-*`: Base **3.0k**, Standard **9**, Higher **3**. Both metadata-only cards retain disabled install controls. These receipts do not show Shared's count, so no Shared-count UI assertion is made.
- Peer sequence 24 records **fixture** malformed/accept HTTP 200; `step118-*` retains the prior B counts/order. This does not mean the app-facing API also returned HTTP 200.
- Peer sequence 35 records **fixture** unavailable HTTP 503; `step120-*` shows counts absent and default rather than B popularity ordering. The app-facing API status is not present in these browser receipts.
- Peer sequence 49 is the actual single-install report POST for Standard, HTTP 503. `step122-*` contains the installed-success toast; `step123-*` retains the Standard card. Independently hashed installed executable is `42f649334296b66910688735d23469d5807e93852257129a06607d404f8304ab`, matching the existing approved Standard artifact. The evidence supports installation not being rolled back merely because reporting failed; it is not a claim about remote-center aggregation.
- Fixed peer generation 2 sequence 62 records bulk `/plugin/install` HTTP 200 after official stop/start, including Standard and Shared with other entries omitted. Raw UID is not recorded. Presence/length/equality are **generation-local only** and cannot prove UID equality across the two cold generations.

## Runtime and safety boundaries

- Fixed responder remains SHA `1bba4bed58901fd41027215c575978ec5e2fa73132f8e66dc76003324a41edd4`, independently reviewed in `../09-fixed-response-static-review.md`: loopback-only fixed responses, no forwarding/DNS/TLS/CONNECT, exact owned paths, bounded and redacted fields. Unsupported requests record 502, not an opened external route.
- Launch source retains the original 39126 HTTP/HTTPS guard and loopback NO_PROXY values. This is a source/launch binding, not a fresh reading of runtime environment values. No forbidden probe was repeated.
- Full settings DOM action `step100-*` was denied and remains denied evidence. Subsequent approved narrow ProxyHost-only observations are distinct; no other settings credentials were inspected by this reviewer.
- **Unresolved native observation limit:** all three native-v2 captures exit 1 at descendant discovery (`exact owned server not found before startup deadline`, scan count 1). No completed mapped executable/loader/libc record exists. Disk binary hashes, launch configuration, official preview receipts, actual UI, and peer traces remain valid evidence, but cannot be upgraded to same-runtime mapped-loader observation. No bypass/retry is proposed here, and this limit does not erase observed behavior.
- **Cleanup complete within UI/lifecycle evidence:** step145 directly confirms the dashboard empty/off; step152 shows only the system job and zero owned rows. The first ProxyHost marker (155) was correctly identified as draft-compatible: the same placeholder/absent clear button can occur before saving. Coordinator performed one narrow official restart/readback, not a product suite rerun. `official-start-cleanup-cold-ready.json` → reload160 → **161** confirms generic proxy placeholder and absent saved-value clear button in the fresh component; **163** confirms empty center and sharing off; **165** confirms only the system job and zero owned rows after cold load. `official-stop-final-cleanup.json` records actual exit 0 / preview stopped. The helper's generation 3 has no requests, consistent with restored settings, but this is auxiliary and not a substitute for the cold UI readings. Helper shutdown is bound to official managed lifecycle/IPC handling, not an asserted cross-namespace process scan.

## Selected-delivery asset check

`ASSET-CLOSURE.md` / `asset-closure.json` document the requested pre-stage snapshot: 194 tracked distribution files, 111 emitted (47 new), 117 older untracked/non-emitted retained files. A conservative current-index dependency graph reaches 134 files and none of the older 117; every reachable file is in tracked ∪ emitted. Thus excluding those old 117 from the selected delivery does not omit any statically identified current UI dependency. The tested binary still embeds all 358, so no bit-identical clean rebuild claim is made. All full-source/asset hashes remained valid at review.

The review distinguishes actual returned UI content, real synthetic-peer events, inherited backend results, static source contracts, and coordinator action records. It does not represent source intent or a mock as a live UI observation. Publish only the purpose-built `publishable-compact.json` and safe selected summaries as appropriate; raw browser/native/fixture records remain private evidence. No commit, push, or ledger change was made by this reviewer.
