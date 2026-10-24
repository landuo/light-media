# Standard framework entrances and execution: bounded stage

This is a framework-only development-branch stage, not a declaration that the framework is complete. CategoryEditor, SystemProcess, MPServerStatus and BrushFlowLanduo business adaptation remains paused. No old invoke/plugin-run/receipt lifecycle is restored.

## Actual interfaces in this stage

- Standard artifacts may declare the generic Vue federation frontend contract. The authenticated production routes resolve the current loaded instance's exact installed package, validate its complete asset inventory and serve the versioned graph. Clones preserve their own code and graph when the source upgrades. The web loader no longer assumes two plugin IDs. Installed same-origin JavaScript remains trusted application code, not a security sandbox.
- Real committed core facts submit six standard event categories: site.updated, site.deleted, site.refreshed, download.deleted, downloadfile.deleted and source-UI subscribe.added. Delivery is best effort after commit, not transaction rollback or durable exactly-once replay.
- Authenticated Web message ingress parses slash commands and dispatches the declared command event/data through the current standard manager. Other external message-provider ingress and non-command/AI/image workflows are not implemented by this slice.
- A shared shutdown admission barrier prevents new asynchronous framework work from entering after Close seals admission, then waits for admitted work. It is not a persistent business queue.
- Explicitly negotiated ServeConcurrent workers can receive simultaneous calls on the same actual object. Per-call host authority, cancellation and a reserved cleanup lane isolate calls. The manager performs close, stop_service and transport shutdown in order. Actual caller lifetimes replace the former fixed method budget only for negotiated workers. Legacy sequential workers retain their previous limits. The SDK bounds ordinary in-flight work and does not make plugin objects automatically thread safe.
- plugin.log.write accepts bounded structured messages with host-owned instance/version/call attribution, uses the existing redaction, history, SSE and export pipeline, and does not invent plugin-run records. Raw stderr byte counters are not represented as worker business logs.
- downloader.tasks.read requires one exact configured downloader identity and an explicit Hash list. Empty selection makes no daemon request. It uses existing qBittorrent/Transmission clients, host-side credentials and request guards; results are typed safe projections preserving integer values. It does not implement task mutation, all-task discovery, add/delete, site requests or plugin business monitoring.

## Evidence and limits

The stage has actual artifact workers and real App HTTP/core entry tests, including race-instrumented parent and worker binaries. The accompanying validation record gives exact frozen inputs, results and retained failures; do not replace that record with a blanket green claim.

The isolated browser pass used production Vue components and loaders with a finite test transport and owned App/artifacts. It exercised all four component factories, native dynamic import/CSS, close events, navigation keys, clone/source upgrade isolation, invalid render-root rejection and recovery, actual unauthenticated API rejection, and cancellation without late mounting. It did not validate production account login or imply that remote JavaScript is sandboxed. Production-entry typecheck and Vite build were executed separately without the test transport.

A preliminary vet run using Go overlays could not chdir into newly added command directories. Its failure is preserved; the exact physical tree subsequently passed vet. Earlier force-stop fixture and execution failures remain evidence and are not silently erased.

## Still incomplete

- Site operations, downloader writes and other business-resource methods are not completed by read-only inventory/task projection.
- Complete source-equivalent DownloadAdded/TransferComplete payload capture and remaining subscription provenance are unfinished; reduced core objects are not broadcast as fabricated equivalents.
- Standard notification transport and remaining external message ingress are unfinished in this commit.
- Immediate background submit and synchronous service-refresh resources require source-faithful current-object admission. A successful original ThreadHelper.submit is independent of a later parent exception; it is not an after-return barrier. Initialization failure with live submitted tasks and all exceptional lifecycle interactions need their own implementation and tests.
- Current concurrent App tests do not prove all getter/service/command/cron/upgrade interleavings or every transport-failure recovery case. Host handlers must cooperate with cancellation; Go cannot force-kill an uncooperative host goroutine.
- No previously paused business plugin is declared delivered by these owned framework fixtures.

Existing unreferenced distribution files and historical binary evidence are preserved in the Git tree. The new index references the newly built assets; the complete Git tree is therefore not claimed to equal the local build's 100-file directory. The old Brush icon remains its original official blob, not a generated placeholder or a locally inspected image.
