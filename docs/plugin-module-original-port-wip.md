# Plugin module original-method port: executable artifact / instance stage

This is an **incomplete development checkpoint**, not full plugin-module acceptance. Main and dev are not changed by this task-branch commit. Original MoviePilot reference remains 8a83efeacd3e26b82be495e17d50146e31c96a60.

## Implemented in this stage

- The existing JSON-RPC SDK now supports negotiated Start / repeated Call / Stop sessions while preserving the old one-shot Run interface. A loaded instance owns its actual process and member state. Each call has fresh authorization and a separate budget; canceled or failed transports are not silently restarted. Business errors may leave mutated object members intact, matching the original object semantics. StopWithHost accepts explicit fresh cleanup authority rather than reusing an earlier invocation.
- The shared App binds sessions by instance, plugin, version and generation. Source and clone use different objects and copied code. Active-call limits do not remain occupied while a process is idle.
- The artifact package acquires an explicitly declared Go executable, ZIP or tar.gz from a Go repository index. It checks manifest, selected version/platform, file paths and digests. Non-force installation backs up/restores on failure; force prepare failure removes code without a restore. Optional backup refresh failures are reported separately. Clone copies have independent file bytes, not hardlinks.
- ReadInstalled validates stored metadata and actual executable bytes. Invalid present code does not fall back to another implementation. The currently supported external host contract is `light-media.sdk-pure/v1`, with no host capabilities. Artifacts cannot relabel or replace exact-version compiled native business implementations. This is not Python package support or an independent publisher-signature system.
- Existing loaded objects remain callable after a failed force download, as in the original manager. This does not permit a new process to start from missing or invalid code. Explicit reload stops the old object and then attempts the real code lookup.

## Actual validation

The final v31 App input passed the same 38 selected roots in ordinary and race mode. These include the prior 34 original-lifecycle roots and four real executable-artifact roots. Source hashes were unchanged after both runs.

The four new roots use actual Go worker executables and owned loopback index/payload servers, not a mocked process: install/save/repeated calls and retained members after an init error; independent clone objects/code/configuration and clean same-ID recreation; a real source-only 1.0-to-2.0 executable upgrade while the clone remains 1.0; force download failure followed by continued use of the original object and explicit reload without valid code.

The complete SDK and artifact packages passed ordinary mode (67 SDK roots and 18 artifact roots). Complete race mode passed all 18 artifact roots and 64 SDK roots but failed three SDK roots: maximum four-way payload, cumulative host-call budget, and parent cancellation during host entry. These failures remain recorded and under finite diagnostic investigation. Earlier focused race passes do not supersede them. No product timeout was increased and no failing root was excluded from the recorded full run.

The original force-failure App red (PUT returned 500 after code removal) and the initial overlay vet directory failure remain retained as historical evidence. Temporary diagnostic instrumentation and untested standard-method adapters are not included in this commit.

## Still unfinished

- Resolve the three complete SDK race failures with causal evidence and the necessary final verification.
- Complete all 21 builtin standard methods against real loaded state: get_state, service and command declarations, API declarations and dispatch. Existing explicit adapters cover only part of the catalog. Copying manifest routes is not yet a complete replacement for the original get_api call.
- Complete shared Form/Page/dashboard/navigation and loaded-but-uninstalled API behavior, preserving original class/running/installed distinctions and authentication.
- Verify the existing market/repository configuration, discovery and version-selection UI. A loopback API installation test does not establish a complete user-facing repository workflow.
- Run the continuous normal-main-program and Chrome lifecycle workflow, followed by the final common ordinary/race regression, web/check/build, vet, Linux build and exact embedded asset verification.

No additional cross-platform, remote-storage or historical-data-import scope is introduced. Paused business-plugin development resumes only after complete module acceptance and the authorized main integration/readback.
