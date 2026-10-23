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
