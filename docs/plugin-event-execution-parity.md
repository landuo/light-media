# Event execution parity, 2026-10-25

This change corrects three observed differences from MoviePilot commit
`8a83efeacd3e26b82be495e17d50146e31c96a60`, `app/core/event.py` (Git blob
`9186902a9f42f1021dea7cf5e15f15775d236deb`). It is a focused G21/G22 result,
not completion of the whole plugin framework or business-plugin acceptance.

## Corrected behavior

- `Event.__init__` uses `event_data or {}`. Initial null, false, zero, empty
  string/list/dictionary, and JSON decimal underflow become `{}`. Truthy JSON
  and arbitrary-size integer values remain intact. A subsequent chain mutation
  to null/false is preserved and is not treated as another Event constructor.
- `__safe_invoke_handler` checks current enablement immediately before execution.
  Broadcast submission no longer discards a temporarily disabled subscriber;
  the execution-time state decides. Chain dispatch still snapshots enabled
  handlers in priority order, then checks each selected handler again. Disabling
  a later handler during an earlier callback suppresses the later call; enabling
  a handler excluded from the original snapshot does not add it to that dispatch.
- `__invoke_handler_by_type_sync` resolves the currently running class by name.
  A callback queued before reload invokes the replacement process, with that
  process's current session and fresh host scope. A stopped/unloaded class is a
  no-op. The Go binding includes the manager's instance ID, implementation ID and
  authenticated clone source, so replacement by an unrelated class/clone cannot
  redirect the queued callback. No old generation or receipt restricts a valid
  same-class reload.

The existing chain unit test had encoded the wrong behavior: it expected an
already disabled later handler to execute. That expectation is corrected;
equal-priority replacement ordering remains independently tested.

## Validation

The extended pinned-source oracle executes the original Event constructor,
dispatch/safe-invoke methods and running-class resolution with owned Python
manager/executor fixtures. Its output is
`testdata/standard-event-target/source-oracle.json`. Python is used only for this
reference test; no Python application/runtime dependency is introduced.

Ten focused App roots passed ordinary and race runs with zero failures/skips.
The ordinary run took 6.940 seconds and the race run 39.871 seconds. Both use a
real acquired `owned-event-target-fixture` executable. The race run uses a
separately built `-race` worker as well as the race-instrumented App. Actual
HTTP configuration, clone and reload routes are exercised, with Base-persisted
callback output and distinct process IDs checked. The controlled legacy method
slot exposes the submission/execution gap; it does not simulate worker results.
Two small identity/ordering tests intentionally use in-process owned records.

The initial App attempt was blocked by pre-existing Darwin `syscall.Mkdirat` /
`Openat` compilation errors, fixed separately with the existing `x/sys/unix`
dependency. The first compiled real-artifact attempt then rejected macOS's
symlinked default temporary ancestor. Tests were rerun under the canonical
repository `work/` temporary directory; the artifact safety checks were not
relaxed. Those failures remain recorded separately from the final pass.

Raw run logs remain in `work/plugin-module-20261025/event/`; the compact record
and event-source hashes are in `docs/validation/plugin-event-execution-parity.json`.
This is selected local Mac/SDK evidence, not Linux, production account, live
provider, all-App or final browser acceptance.

## Remaining G21/G22 boundaries

Original callback errors also enqueue a MessageHelper system notice and emit
`system.error` with exception text/traceback. The current SDK transports only a
safe error code (or mutable-event failure flag); full source diagnostics are not
implemented here and no worker traceback is fabricated. Cross-process JSON does
not reproduce Python nested-object identity sharing. The original broadcast
priority-queue/consumer policy and non-plugin global/module subscriber ecosystem
are not established by this focused callback verification.
