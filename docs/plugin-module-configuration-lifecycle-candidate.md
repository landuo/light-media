# Common plugin module: configuration and lifecycle candidate

This is an intermediate task-branch checkpoint, not final module acceptance or a dev/main integration. Other plugin development remains paused.

## Current behavior

The common module uses one authenticated instance and its existing configuration. Opening configuration and supported read projections creates no execution job. The first explicit original remote configuration operation can initialize the same instance and continue through its original API without requiring a manual reload or empty save. Business enablement and execution admission remain separate from loading.

Explicit settings saves retain revision CAS and authenticated completion checks. Successful results come from the actual applied projection; an interrupted response after commit must not imply that the configuration was never saved or automatically replay it. Exact compiled business validators remain responsible for their private revision receipts.

Ordinary uninstall retains configuration and KV. Authenticated clone uninstall clears only that clone's configuration/KV; explicit same-ID creation advances generation and cannot revive its previous private history. No user media or configured directories are deleted. Host-side compiled declarations neutralize only known copied one-shot execution intentions, including the uninstall plugin's selected-ID array; unknown custom values remain intact.

SeedRescue and AutoSignIn private read decoders use the common authenticated read session. The common callback verifies the full requested installation identity, including desired-enabled state and sealed manifest, before invoking private readers. Current read cancellation has no queued execution; legacy authenticated read-run recovery remains separately tested.

## Evidence and open gates

On the exact 5,799-file frozen candidate, the focused correction batch passed 10 roots / 33 test nodes in ordinary mode on 2026-10-23 at 15:11:56 UTC, with no input drift. It includes full snapshot identity rejection, current and legacy read cancellation, legitimate normal restart, clone review removal/recreation, authenticated null completion, and array-trigger protection.

The first complete ordinary attempt ran 459 selected roots: 453 passed and 6 failed. Those failures are retained. Four required corrections to obsolete or inaccurate test expectations, one exposed a real missing common snapshot identity check, and one exposed a cancellation-observation mismatch after reads stopped creating jobs. Earlier smaller ordinary/race results are historical evidence, not claims that all latest production bytes passed race.

The second complete run selects 460 real App test roots. A no-argument fixture helper is no longer counted as a standalone acceptance test; the real normal-restart and array-intent roots were added. This complete ordinary/race run, separate protocol/schedule gates, final vet/Linux builds, and same-binary Chrome flows are pending at this checkpoint. The isolated frontend passed 233 tests, type checking and build; all 56 regenerated assets matched the frozen dist, and 200 older existing assets were retained.

## Scope

MoviePilot's fixed source 8a83efeacd3e26b82be495e17d50146e31c96a60 is the behavior reference. Existing synthetic original-method oracles are not represented as a complete original-application execution. This project supports fresh Linux/local configuration, with compiled Go plugin updates only where an explicit conversion is available. Old-system data import is not an acceptance requirement. No second cold-draft interface or ABI is included.
