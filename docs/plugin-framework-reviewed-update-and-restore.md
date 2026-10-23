# Reviewed builtin updates and cold restoration

This stage extends the compiled builtin plugin framework. It is not an external
repository installer or a promise to execute arbitrary historical migrations.
Unknown version transitions remain explicitly unsupported.

## User-facing lifecycle

- A supported builtin migration changes only the selected instance. The current
  implementation has an audited CategoryEditor 1.2 → 1.3 migration.
- Retained enabled/one-shot intent does not grant permission to replay it during
  migration. Such an instance is displayed as requiring configuration review.
- Opening, refreshing, closing, startup reload, and programmatic config writes do
  not activate a held migration. Only a fresh authenticated configuration save
  against its exact revision can release the gate and queue initialization.
- Uninstall preserves the exact held configuration and instance identity. The
  marketplace offers **恢复为待确认** when the stored version is the exact compiled
  target and its authenticated metadata is valid. This action reinstalls cold:
  it does not execute initialization, send messages, or consume a one-shot flag.
- A restored instance remains pending review. The user can inspect configuration
  and explicitly save it to activate that instance. A stale repeated restore
  request cannot reinstall it again or silently switch to the default instance.
- Corrupted metadata, unavailable compiled targets, and unsupported schemas are
  not offered as safe restoration. Ordinary installation is not a bypass.

## Plugin implementation contract

Plugins provide their business implementation and compiled declarations; they
must not manufacture instance suffixes, lifecycle admission evidence, update
review authority, or run-completion permits. Framework storage, configuration,
schedules and references remain scoped to the authenticated instance.

A business host operation may replace a run's durable snapshot after committing
an authenticated config-revision transition. The executor retains its original
private claim separately for cleanup. A refreshed database run is not a new
claim. Plugins must not copy private execution capabilities between snapshots.

Normal plugin business completion and lifecycle cleanup are distinct: the
framework records a durable outcome, waits for owner/process cleanup, and only
then releases the admitted run. Reset uses the framework fence/cancel/join path;
unknown outcomes must not be reported as a successful reset.

Host metadata is not part of the plugin's immutable compiled Manifest. Shared
readers authenticate host lifecycle, lineage and update-review fields before
strictly decoding the remaining business manifest. Unknown business fields and
malformed authenticated host state remain errors.

### Compiled update declaration

The current extension point is builtin.UpdateMigrationFor, returning an
UpdateMigration with FromVersion, ToVersion, ValidateConfig and
RequiresExplicitSave. The declaration validates a known target configuration and
identifies retained execution intent; it does not receive SQL, a Store, a
network transport or instance lifecycle authority. This release's declaration
retains the existing CategoryEditor configuration bytes and data. It does not
claim to transform arbitrary native owner schemas or download new plugin code.

Normal authenticated UI configuration save remains the activation boundary.
Plugins must not call restoreReviewedPlugin, manufacture review permits, or
release execution attempts themselves; these are trusted host lifecycle APIs.
Use the SDK's existing instance-bound business capabilities instead.

## Restart and native transaction outcomes

The common attempt journal does not replace a plugin's existing authenticated
business receipt. Recovery reads those receipts through bounded typed adapters:

- Proven completion can reconcile a durable result without executing it again.
- Proven unknown effects remain blocked. An authenticated IYUU unknown outcome
  retains its original diagnostic; it never becomes permission to retry.
- A reviewed, exact SubscribeGroup 2.8.7 transactional path may retry the same
  invocation only when its business receipt is absent and its original claim,
  configuration, generation and event scope are still authenticated. Event
  retries additionally validate the original batch, delivery and sealed domain
  snapshot. This exception does not apply to arbitrary plugins or actions.

The no-business-effect handoff is sealed against the original attempt and is
rechecked inside the next claim transaction before a new linked attempt exists.
A running process cannot mint that restart permission. Prior completion records
remain immutable. The private plugin_run_attempt_recoveries and
plugin_run_attempt_retries tables add authenticated evidence rather than
rewriting original claims or completions. Older binaries do not understand
retry proofs; opening this database with an older binary is not an established
rollback or recovery procedure. Missing, contradictory or damaged evidence stays quarantined.
SubscribeGroup's Added path may refresh a derived media metadata cache before its
business transaction; that repeatable cache refresh is not represented as an
external notification or download side effect.

Reset checks the exact identity and fence before native read-only integrity
validation. It must establish run quiescence before any native schema/data writes
or deletion. Trusted Store reset keeps its historical inactive-instance contract;
the public HTTP route retains its existing inactive-instance response.

## Verification status (2026-10-23)

This task-branch milestone was checked against frozen source snapshot
c157c01801cde44cfee24c29c5fe61ae3fa667413961b1d7b81ce3c571f96ce7.
The affected local suite covers 225 top-level tests / 1,492 test nodes in both
ordinary and race modes, using exact unchanged-test evidence plus the separately
revalidated final test-only request-lifetime correction. One private-environment
IYUU stdin fixture was not supplied or executed and is not counted as passed.
Three package vet checks, the actual cmd/server build and the tagged browser
fixture build passed. The unchanged frontend passed 219 tests, type checking and
production build.

Assistant cloud Chrome validated the final restored-instance flow against the
actual ordinary cmd/server SDK worker: the exact uninstalled clone was restored
cold, refresh preserved its pending state, opening and cancelling configuration
left zero runs, and one explicit save produced exactly one completed run and the
expected owned category file. The target reached version 1.3 / generation 4;
its sibling remained version 1.2 / generation 1 with unchanged configuration, and
the source remained version 1.3 / generation 1. The pending gate cleared and the
consumed one-shot flag was false when reopened. The owned fixture passed and
exited cleanly with its child reaped.

Historical failures remain recorded rather than rewritten as successes: an old
grouped NoticeV2 timeout, a legacy full App-test-race worker startup failure, and
a grouped nested Form case that exceeded its test context. A diagnostic rerun
of the latter did not reproduce it. The test request now starts after unrelated
fixture preparation, retains its original five-second caller budget and all
eight rejection assertions, and passed ordinary/race; this is not a claim to
have explained the previous 27-second observation or changed product timeouts.

This is not yet aggregate-branch integration acceptance. Integration must retain
the main branch's narrow site dependency lookup, runtime support and UI fixes,
and MergeMessageNotify's actual host callers; the combined build and affected
runtime/Reset checks belong to that separate integration step. Arbitrary historic
plugin versions and unreviewed business-owner schema migrations remain unsupported.
