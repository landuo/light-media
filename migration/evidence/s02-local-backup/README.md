# S02 local backup material — finite backend slice

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
