# Shared plugin framework: stage-one acceptance status

This is a bounded implementation milestone, not completion of generic reset,
running-I/O cancellation or compiled-version updates. The clone UI milestone is
published on `plugins/framework-lifecycle` at `71875266`. This commit records the
cumulative backend stage on that task branch, not an aggregate integration or
acceptance of the full framework.

## Implemented contracts

The host owns instance identity, atomic disabled/cold clone creation, independent
configuration namespaces and immutable lineage. Compiled manifest versions remain
code identity; custom displayed versions do not install or update executable code.
Plugins use the authenticated instance context rather than derive clone IDs.

The shared Form dispatcher selects preview, installed-cold, started and legacy
unknown modes from trusted stored state. Missing or corrupt business evidence is
not permission to manufacture initialization. Configuration transitions require
compiled declarations and a same-transaction business projector. The public
projector facade exposes only Data, SaveData, Config and VerifyInitialization;
it exposes no SQL, Store or transaction commit capability. Failure is sticky and
callbacks become unusable when their admitted lifetime ends.

Configuration-save origin, immutable admission receipts, verified revision
transitions and event admission are host-owned. Worker-supplied flags do not
establish an explicit save request. Common Form mutation guards run before
preparation and durable side effects. This is not a sandbox for arbitrary trusted
Go, raw SQL, OS calls or unrelated captured contexts.

## Verification, 2026-10-23 UTC

The frozen acceptance inventory has 355 roots: 354 locally runnable and one
specific external FNOS fixture requiring an environment and private stdin not
provided for this run. It is not counted as passed or treated as a network ban.

Ordinary coverage: all 354 local roots passed. Race coverage: 353 local roots
passed; one NoticeV2 root had two deadline failures in its 60-root group and
remains unresolved. The later single-root diagnostic passed without reproducing
those deadlines. A single unmodified current aggregate (`a2d93ce8`) comparison also passed.
Neither result erases the original failures or establishes their cause. All failed and partial runs remain retained.

The actual SDK 17 MiB configuration root passed on final candidate bytes in both
modes: 49.455 seconds ordinary and 462.644 seconds race package elapsed. Its data,
actual SDK behavior and product budgets were unchanged. The 15-minute package
watchdog is test orchestration only, not an increased product deadline.

A separate SiteStatistic failure was traced to a test opening a synchronous Form
before claiming an already-due one-shot schedule. The real scheduler correctly
reported a misfire. The test order was corrected without changing its three-second
schedule or one-second grace, and gained before/after complete state and zero
network assertions. The affected root passed ordinary and race; unchanged roots
were associated by exact source/function/helper evidence rather than rerun or
misrepresented as a clean original group.

## Still required

- Resolve or accurately bound the remaining NoticeV2 failure before declaring the
  local stage-one regression complete.
- Integrate the stage with separately accepted RuntimeSupport, VDivider and public
  system-message timestamp changes, then rebuild the combined frontend.
- Implement and verify real running-context cancellation/join for reset,
  uninstall and shutdown. Durable generation/configuration cleanup alone does
  not prove blocked I/O has stopped.
- Implement real compiled-version migration/update and rollback hooks. No plugin
  marketplace, external repository installer or dynamic executable updater is
  part of this milestone.
