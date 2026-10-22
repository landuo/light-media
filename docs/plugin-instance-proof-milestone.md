# Plugin instance proof and default-normalization checkpoint

This is a backend development checkpoint on `plugins/framework-lifecycle`,
based on `d3176ff7`. It is not complete lifecycle acceptance and is not merged
into `dev/plugins-continuation`. The exact verification record is in
`plugin-instance-proof-verification.json` beside this document.

## Shared host contracts implemented

- `NormalizeInstanceDefaults(ctx, installation, revision, missingDefaults)` is
  a missing-key-only CAS transaction. Existing false, zero, empty and null values
  are preserved. It does not activate a cold clone or queue initialization.
  Writes require the exact compiled manifest to declare `config_defaults:
  "pure/v1"`; persisted metadata cannot grant the declaration. SystemProcess and
  CategoryEditor declare it. Native business owners without that reviewed
  contract fail closed. Busy and reconciliation-pending instances are rejected.
- Normalization propagates the configuration revision into eligible pending
  inputs, schedules, event subscriptions and registered API ownership atomically.
  It preserves event and scheduling boundaries rather than replaying business
  work. Hooks for stateful native-owner projection are subsequent work.
- Authenticated configuration PUT requests establish an instance-bound host
  request context. Their initialization admission can write a sealed,
  persistent config-save origin in the same transaction. `ConfigSaveOrigin`
  checks the actual active run and configuration. A worker's `config_saved`
  flag, ordinary reload, clone creation or form default write is not proof.
  Old runs without origins remain non-save; corrupted proofs fail closed.
- Host configuration changes append sealed, identity-bound revision/input/config
  transitions instead of rewriting the original origin or deferred receipt.
  Active deferred continuation validates the continuous chain against the
  current run and current configuration. It rejects missing edges, substituted
  rows, cross-instance evidence, stale runs and successor evidence. The chain
  has a bounded length. This does not detect rollback of an entire consistent
  database snapshot.
- Cold deferred recovery separates historical repair from current execution
  authority. It can repair an authentic old terminal receipt after reset or
  uninstall without reauthorizing that generation. Started/uncertain work is
  not replayed automatically. Invalid native uninstall evidence is quarantined
  or rejected, not turned into success. The existing native uninstall revision
  bridge is narrowly retained only when no generic transition exists.

These are host APIs, not new worker RPC powers. Plugins retain their business
validation and required compensation/stop hooks. No plugin-specific clone
branch, external plugin repository, remote storage or production-media deletion
is introduced by this checkpoint.

## Verification and unresolved failure

On the frozen V5 bytes, ordinary verification passed 78 top-level tests and
222 including subtests. Race partition A passed 48 top-level and 172 including
subtests. Race partition B passed 29 top-level and 49 including subtests, but
one existing HTTP uninstall/reinstall form test failed with
`PLUGIN_FORM_FAILED` after 10,504 ms. The full race set therefore did not pass.
No race-detector report was present in that failure.

A separate diagnostic copy enabled the existing safe runner phase observer for
PluginUnInstall without changing the worker entry point, test assertions or
10-second form budget. One bounded rerun of that exact top-level test passed;
its four real form calls took 1,564–2,327 ms. The original failing invocation
had no PluginUnInstall phase capture. This successful rerun does not establish
the phase or cause of the original timeout and does not replace the red record.
The diagnostic changes are not part of this production checkpoint.

Earlier fixture failures are retained: the initial V3 fixture had an invalid
CategoryEditor configuration; the initial V5 native-uninstall fixture omitted
self-selection. Both were corrected before the final frozen ordinary inputs.
Race A's complete input capture was recorded after process launch because the
initial evidence script failed; the same complete source list was checked
against the pre-existing frozen ordinary input and again after completion.
It is not represented as a pre-launch capture.

## Compatibility and unfinished work

- New anchored deferred receipts contain a config hash. Older strict decoders
  reject that field. Downgrading to an older executable over the changed live
  database is not declared safe; use a compatible executable or matched backup.
- Full unified reset, update/version migration, compensation ordering and
  end-to-end rollback remain unfinished. This is not a completion claim.
- Stateful native-owner form projection, authenticated cold-initialization
  markers and common event-admission cursor proofs are not delivered here.
- MergeMessageNotify has not been registered as a production caller of these
  new contracts in this checkpoint. Foundation tests do not substitute for its
  actual business integration and browser acceptance.
- Clone UI/browser verification was performed separately, but its frontend
  publication is not part of this commit. No old frontend tree is used to
  overwrite this backend checkpoint. Final integration must rebuild the UI on
  the then-current aggregate branch and repeat relevant gates.
- V5 has the ordinary and race evidence listed above, not a claim of a new
  complete standalone server build, vet pass or all-package acceptance.
