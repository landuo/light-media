# Original plugin module port: partial implementation checkpoint

This is an unfinished task-branch checkpoint, not an acceptance release, and is not a new main/dev baseline. Independent plugin business work remains paused.

## Product source

MoviePilot backend commit `8a83efeacd3e26b82be495e17d50146e31c96a60`, core `app/core/plugin.py` and API `app/api/endpoints/plugin.py`, plus its `app/scheduler.py`, `app/command.py`, and event manager, define the method order and exceptions. Original plugin methods are retained as the business implementation reference. The Python-to-Go executable/storage adaptation does not authorize changing original configuration values or call counts.

The original combined-method AST oracle has 12 passing synthetic cases, with original method bodies unmodified. It is not a full original-server test. Nonempty clone configuration is copied with `enable=false` and `enabled=false`; no `onlyonce`, selection, or other unknown field is cleared. Core reload, optional direct second initialization, and endpoint reload yield three initializations for a nonempty configuration and two for empty/absent/null. Fresh configuration is read at each original read point. First start exceptions are caught; direct second initialization exceptions return failure with the prior running object and partial state retained. Original success is not proof that an instance loaded successfully.

## Implemented and connected slice

The actual clone HTTP endpoint calls synchronous initialization through the existing compiled SDK/host path, records real loaded calling objects, emits asynchronous PluginReload callbacks, and invokes the separate registration entry. Owned MP status probe resources are actually canceled for the selected instance. CustomHosts' original callback filters the event's plugin ID to its own class/instance name and invokes its real initializer. Event-disable failures preserve the running object. Start stores the initialized object before its state/event update, preserving that stored object if the subsequent operation fails.

Transfer and IYUU initialization expose their actual applied Config objects to the calling object. State/service getters do not read a subsequently saved but unapplied database configuration. Failed partial objects remain real objects; checkpoint parsing is not relaxed to reconstruct invalid partial state. Public service registration and private initialization jobs are distinct.

## Explicit unfinished work

- Complete the remaining compiled plugins' actual standard state/service/stop methods and initialization resource lifetime mapping. Unsupported state/service adapters are explicit errors, not claims of completion.
- Complete original public scheduler logical-ID/collision behavior. Current composite instance/service storage IDs are the existing Go execution binding, not identical Python job IDs.
- Complete full preset/plugin/other command merging, CommandRegister interception/subset/force semantics, and actual command-chain registration. The asynchronous instance-bound plugin command map is only an initial connection.
- Complete all original lifecycle entry points and startup integration, including ordinary loading, explicit configuration save, reset, uninstall, update, and original return/exception semantics. Older pre-port code still exists and is not newly validated by this checkpoint.
- Full module ordinary/race, normal executable, browser and final integration gates remain outstanding. This checkpoint does not reuse the old proposed framework's full-suite results as proof of the literal original port.

Old trigger-neutralization/lazy-write/cold-draft proposals and their evidence remain preserved historically. They are not the product contract for this port. All tests use owned synthetic configuration, files, and resources; no real user task or data deletion is performed.

## Checkpoint test result

The frozen v9 slice passed the same 11 roots / 31 leaf scenarios in ordinary and race modes on Linux, with zero input hash drift and both sessions reaped. The earlier v8 IYUU fixture failure used a Go integer instead of the real JSON-number input type; only that test input changed, and the failed output remains preserved. Full-module race, normal executable and browser acceptance remain outstanding.

## Subsequent lifecycle endpoint slice (not full-module acceptance)

The actual configuration HTTP entry now saves the original dictionary, initializes the same loaded calling object, and registers services/commands/APIs. A missing running object has the original init_plugin no-op behavior. A stale transport revision is not an original save prerequisite. Save commits before init: an init exception preserves the saved configuration and the original object rather than inventing a transaction rollback. Repeated initialization keeps the actual Transfer/IYUU object, including the original empty-config member retention.

App.Start now invokes the installed-list manager startup, including business-disabled and absent-configuration instances. Original plugin_order metadata is separate from sealed installed manifests. All 21 values were read from original sources; Cloudflare 1.5.1 uses the exact same-version official original source, but a local whole-file installed hash was unavailable for comparison. The retired internal desired_enabled staging bit is normalized for installed initialization; no business enabled configuration value is changed. A running object is still distinct from HTTP success and from business get_state.

Reset sends its synchronous PluginDataReset chain before stop. The chain follows original priority/enabled checks and catches individual handler errors; the Reset endpoint ignores its boolean result. The 21 compiled original classes have no such registered callback. This does not assert a search of every possible third-party original platform subscriber. Config deletion precedes logical-data deletion; a second-operation SQL failure keeps the already-completed config deletion and stopped object, without reloading. Successful Reset then executes the ordinary stop/start/reload-event/register sequence.

Native logical data is isolated into a new internal generation with empty active records. Existing authenticated roots are storage-format bindings, not the old NoUnknown lifecycle permission gate. Old sealed audit records are retained outside the new active namespace. Owned backup ZIP bytes and filesystem directories are not deleted. A real owned archive write with a lost acknowledgment is covered separately: explicit logical-data clearing does not replay it or relabel the old uncertain run. This is not evidence of all native plugins' complete reset/init(None) resource behavior.

Remaining work is unchanged in scope: all catalog standard-method adapters and native resource lifetimes, full ordinary install/update/uninstall endpoints, complete scheduler/command semantics, native initialization with absent configuration for every adapter, full-module tests and real browser verification. Independent plugin business tasks remain paused.

The frozen v16 slice passed 20 roots / 42 leaf scenarios in both ordinary and race modes, with zero source hash drift and both processes reaped. Earlier slice outputs and source manifests remain preserved. No main/dev ref changes are part of this task-branch checkpoint.

The remaining original class-registry versus running-object distinction, including saves or forced resets of unloaded/uninstalled identifiers, is not claimed complete by these installed-instance endpoint tests.

## Registry and uninstall slice (current partial checkpoint)

The original class registry, loaded objects, installed list, and available code are now distinct in the actual HTTP entry. Saving without a class does not persist configuration but keeps the original endpoint success result; saving with a class but without a running object persists it without initialization. Explicit reload/reset can load an available compiled primary plugin that is not installed, while normal startup still visits only the installed list. Unknown identifiers retain the original successful no-object result without creating storage or runs. The exact in-memory original initialization binding permits the existing internal SDK run without pretending that the plugin is in the installed list. This is not a general execution authorization for arbitrary uninstalled runs.

The uninstall entry follows the original separate operations: installed-list removal, API removal, public-job removal, conditional clone logical-data/code removal, folder removal, and stop. Clone detection uses the current imported class, not merely historical installation metadata. An already-stopped clone without a class retains its configuration and code, matching the original check. A compiled clone code descriptor models the copied Python package's existence; successfully removed code is not recreated merely because historical metadata remains. Ordinary compiled code remains available for explicit reload. The source and sibling objects/configuration are protected by actual tests.

Clone creation now requires an imported source class rather than an installed flag. A loaded ordinary plugin removed from the installed list can still be cloned. An installed identifier without a class cannot be cloned. A deleted clone code alias can be recreated with the same ID, a fresh internal storage generation, and no old logical data. Original configuration copying still changes only enable/enabled, preserving all other fields.

Folder removal follows the original first-occurrence list removal and logged-error continuation; this small helper has static source comparison but no dedicated nonempty-folder failure matrix yet. Public-job deletion tests in the SystemProcess uninstall cases involve an empty service set, so they do not establish nonempty scheduler behavior.

The previous v17/v18 explicit uninstalled reload/reset failure was PLUGIN_LIFECYCLE_INVALID before SDK entry. The fix is the exact host-created initialization binding in the existing lifecycle marker check, not weakening arbitrary worker claims. Those failed outputs remain retained. The same earlier v19 23-root batch subsequently passed ordinary and race.

Current v21 ordinary and race results each pass 27 roots / 54 leaf scenarios, zero failures and zero source drift, with both successful sessions reaped. The first race session was lost during an environment interruption with empty output and no terminal result; it is not counted as passed or failed. The same frozen input was resumed into separate logs and passed. Full-module acceptance remains outstanding.

Still required: installed-independent services/commands/API execution for genuinely loaded objects beyond these representative instances; all catalog standard-method adapters; original install/update behavior within explicitly supported Go compiled artifacts; complete scheduler/command semantics; full final tests and normal-server/browser lifecycle verification. No Python market package execution, complete package downloader, or arbitrary version compatibility is claimed. No independent plugin business task is resumed by this checkpoint.
