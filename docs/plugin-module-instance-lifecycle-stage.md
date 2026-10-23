# Plugin module: instance configuration reads and clone removal

This is an independently verified stage, not completion of the whole plugin-module parity review. Base: dev d8394e2c19b6df3a20a912bc3b61858d91604475. No change to main or dev is included by publishing this task-branch stage.

## Original contract and scope

Reference: MoviePilot commit 8a83efeacd3e26b82be495e17d50146e31c96a60, app/core/plugin.py and app/plugins/__init__.py; the original uninstall API distinguishes ordinary and cloned instances. Original-method AST oracle suites passed 13 + 9 synthetic cases, using temporary owned data and injected scheduling/reload collaborators, not a complete original server.

Loaded/configurable is not the same as business enabled. This stage shares the existing authenticated instance view and read-only scope across install, clone and reset. Compiled host declarations choose configuration readers; the common router contains no plugin-specific selection. The BrushFlow adapter decodes its private history into the original status/detail model; a second compiled-schema fixture verifies the common selection and read-only boundary. Reads do not enqueue initialization, execute tasks, change data or call external services.

Ordinary uninstall retains configuration and KV. Authenticated clone removal clears only that clone configuration/KV in the existing lifecycle transaction. Unknown or active attempts prevent destructive removal; transaction failure rolls back the entire lifecycle. A removed clone ID may be recreated only under its authenticated source lineage, with a fresh generation. Old authenticated native history remains audit evidence but cannot reappear as the new instance history. No media, user-specified directories, dependency files or private external resources are deleted.

## Verification

The 15-root focused ordinary and race groups both passed 22 named test nodes. A separate real native-history root passed ordinary and race: compiled SDK initialization and task execution produced native history/receipts, clone removal and same-ID recreation hid all old history, while prior sealed state/root/receipt bytes stayed intact and the owned network trap saw zero requests. Input files were checked unchanged before/after each group. An existing frontend graph root was also selected and passed; this is not a new real-browser claim.

Preserved earlier evidence includes the initial 404 read failure, a fixture identity mismatch, a temporary-directory ENOSPC failure, the initial management-envelope fixture mismatch, and the actual clone cleanup/recreation regression. They are not retroactively relabeled as green.

## Remaining original-module parity work

Explicit remote configuration writes are not delivered in this stage: settings currently has a captured 404 regression and its implementation remains outside this tree. The next step must reuse the original business handler and the existing shared config/CAS/init transaction, report committed-but-not-confirmed execution truthfully, and not create a second draft UI or lifecycle state machine. Create/update/state, explicit deletion cleanup, loading/init observable parity and the remaining original API/event/command/scheduler/update/reset/uninstall review still require their exact affected validation before the whole module is declared complete.

Existing claim/unknown safety remains common host infrastructure. Go-only updates require a compiled declaration; this is not support for arbitrary Python marketplace packages. Linux/local usage and fresh configuration are the agreed scope; old-system data import is not an acceptance gate. Other plugin tasks remain paused.
