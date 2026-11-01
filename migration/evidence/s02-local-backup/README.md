# S02 local backup material

## Current S02 material/v1 completion (2026-11-01)

The exact original S02 parent scope is independently closed by the new large-material source → codec/filesystem → SDK/installed worker → normal dual-host CLI evidence, while retaining the directory-selection/reconstruction/age-cleanup credit below. See [current evidence and reproduction](../s02-material-v1/README.md) and [SDK/lifecycle contract](../../../docs/plugin-local-torrent-backup-material.md). Initial parent closure SHA: `3eff82e1dd37f0886c7d45ce61bda59acd96f19314cb80bf0ac0f917f0c6c299`; after the affected-test maintenance described below, successor closure SHA: `ae7d972c6e9873a2d5baac9c8f276fccf9ebfa4f9b311943eccf6015e3ce32ec`.

New host + new SDK negotiate `material/v1` for S02: no additional 16 MiB material-total cap, fixed bounded frames and checked complete length/SHA. Public `LocalTorrentBackup.Prepare` still returns `[]byte` and materializes O(n); this is not O(1) memory or an unlimited ordinary-message API. Unnegotiated old peers retain their original bounded prepare/read/release protocol; failed negotiated operations never downgrade/retry. Ordinary payload/frame/wire/callback/deadline budgets remain unchanged.

Only exact invocation-owned random files under `temp/plugin-material-spool` are cleaned on scope termination. The separate original deterministic `temp/plugin-torrent-backup/<hash>.torrent`, backup/library bytes, and S12 cache are not deleted by spool cleanup. Original S02 age cleanup remains separate. This completion does not remove S03/S12 material limits or complete unrelated parents.

Build the current fixture from `./cmd/owned-local-torrent-backup-fixture` and set `OWNED_LOCAL_TORRENT_BACKUP_WORKER` to that artifact. The two existing `TestSharedLocalTorrentBackupActualWorker*` roots have been maintained and actually rerun against current material/v1, alongside the separately accepted new `TestSharedLocalBackupMaterialLargeWorkerAndCold` root. All original business/permission/backup/temp/cold root bodies remain unchanged; common helpers now require actual grant metadata, one ordinary prepare_material callback, invocation-end spool cleanup and stale HostCaller denial. No legacy handle exists in this mode, so its released-handle observation must be absent. Explicit unnegotiated compatibility remains exercised by `TestLocalTorrentBackupWireAndBoundary` and `TestLocalTorrentBackupPagedBytesAndCleanup`. The historical raw results below remain records of their original execution, not the updated-protocol result.

## Historical original bounded delivery record (unchanged evidence)

Everything below describes the earlier bounded artifact and its original verification date; its old 16 MiB/full-S02 and source-native-runtime statements are superseded only for current S02 by the section above. Its raw evidence is retained, not rewritten as a new execution.

Base: `b9e667cbc9532cb996993af30531327af3dcc541`; fixed delivery: `plugins-refactor`. This is a locally verified backend candidate, not a remote-push or CI receipt.

## Implemented scope
Purpose-bound SDK/actual host capability `downloader.backup.material`: administrator-selected local backup roots, original qB `.torrent` / `.fastresume` tracker reconstruction, Transmission raw bytes, invocation-owned paged transfer, and source-style private temporary material retention/cleanup on orderly shutdown. `temp_file_days` is optional, defaults to original 3, preserves omitted settings and explicit 0/negative values. Cleanup runs only inside this feature's private sandbox after producers join; it never cleans backup/library/source roots or other image/package caches.

Source consumer is the existing pinned `testdata/plugins/torrent-transfer-1.10.3/torrenttransfer.py:785-869`; core source is MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` (`SystemUtils.clear`, config default and shutdown catch/continue). Historical consumer/source/codec coverage is retained, not reset.

## Executed evidence
- Latest exact candidate: pure 2 roots, SDK 2 roots, actual installed-worker App 4 roots (including cleanup 2), host race 2 roots; normal compiled worker, not a race-built worker.
- Actual normal CLI: hosts 25→51, workers 42→66, 14 control requests / 5 business calls; 182-byte source reconstruction and 1,048,579-byte two-page transport; explicit host error then same-invocation recovery; graceful cold restart, unchanged backup bytes/mtime, fresh temp retained. Both host Wait statuses were 0.
- Original cleanup capture is six unchanged selected-AST cases with recording filesystem/clock ports. Separately, target cleanup performed actual deletion only on newly-created owned test files, preserving fresh material and non-sandbox sentinels; no source OS cleanup claim.
- Shared dispatcher request INFO repair is also included: original before failure retained, normal/race each one root with three cases and prior dual-host/four-business CLI retained by independently checked unchanged semantic dependencies. The original whole compilation closure differs; it was not rerun in this candidate. No unfinished native browse SDK is included.

## Boundaries
Not full S02 / all 61 groups / business TorrentTransfer migration. Existing managed roots (`AllowedRoots` plus library roots), private/no-follow authority and per-material 16 MiB transport guard remain; the original source has no identical size cap, so arbitrary-size equivalence or blanket approval is not claimed. No real downloader daemon, actual download, NAS, browser/UI, source native-helper execution, CI or remote-delivery claim. No new permission/inode/symlink/path-escape probe was run. Fresh-temp retention in CLI is not a CLI age-threshold test.

## Reproduction and evidence retention
The Go tests and owned fixture are tracked in this commit. Build `./cmd/owned-local-torrent-backup-fixture`, set `OWNED_LOCAL_TORRENT_BACKUP_WORKER` to that exact artifact, then run the two `TestSharedLocalTorrentBackupActualWorker*` tests. The normal CLI driver is preserved unchanged as an execution artifact; its original absolute paths/source hashes and explicit owned-test safety checks must be reviewed before a new authorized execution, not blindly rewritten.

This compact publication keeps raw test events, original selected-source observations, exact execution results/receipts and independent readbacks. Full immutable compilation maps, generated input copies and raw non-public runtime evidence remain in retained cloud evidence and same-account Space WIP packages: `libfile_018317da01b08191b617758b54e454d2` and `libfile_396aff5807588191bba4f6b79aa4366e` (v0); the latter depends on the former. No DB, executable, real credential or backup payload is published. Synthetic INFO cookie-bearing full responses are deliberately excluded; their existing independent readback and hashes are retained.
