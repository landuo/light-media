# Plugin framework: current integrated contract

This document indexes the reviewed framework contract as of 2026-10-23. It
supersedes the unfinished-status sections of earlier milestone reports, not their
historical test records. The integration combines aggregate `61cc7d5c` with
framework `e1fd711e`; unpublished MergeMessageNotify and SubscribeGroup batches
are not included.

## Author-facing APIs and lifecycle

- [Practical plugin author guide](plugin-author-instance-example.md): real handler,
  identity and instance-bound host calls, declarations and required tests.
- [Instance/clone contract](plugin-instance-framework.md): authenticated instance
  lineage, namespaces and shared-reference rules.
- [Save origin and immutable revisions](plugin-instance-proof-milestone.md).
- [Typed Form projection facade](plugin-form-projection-facade.md): only Data,
  SaveData, Config and VerifyInitialization; no raw SQL/Store or commit access.
- [Trusted Form sessions](plugin-form-session-milestone.md): Preview/Cold are
  read-only, retained state is authenticated, and callback errors remain errors.
- [Cancellation and Reset](plugin-framework-runtime-reset-stage.md): exact claim,
  cancellation, owner/process join and durable-result validation.
- [Reviewed updates and cold restore](plugin-framework-reviewed-update-and-restore.md):
  preserved intent, explicit authenticated save, restart/native receipt recovery.

The framework supplies clone/lifecycle behavior. A business plugin must not
implement suffix-based identity, a second instance registry, forged save origin,
or a private reset bypass. Business effects and uncertainty remain governed by
its authenticated receipt and reviewed transaction boundaries.

## Integration guarantees and evidence

The joint source retains the aggregate's Linux/local RuntimeSupport notice,
original RemoveLink VDivider rendering and system-message absolute timestamps.
The site dependency query first narrows by PluginID and then authenticates clone
lineage; unrelated malformed namespaces cannot poison a narrow lookup, while
unmarked duplicate defaults and missing lineage still fail closed. IYUU's actual
ordinary-server selection and the framework's real claim-completion fixtures are
both retained. Existing hashed web assets are preserved; the index and new assets
are rebuilt from the joint frontend, not copied over from either old branch.

The exact integration verification is recorded in
[plugin-framework-main-integration-verification.json](plugin-framework-main-integration-verification.json).
It distinguishes the original framework's 225-root affected-suite evidence from
this integration's 18-root cross-component ordinary/race checks, joint frontend,
actual production worker/build and browser checks. It is not a full-repository or
remote-CI success claim. Race instrumentation applies to the test host; fixtures
explicitly selecting ordinary cmd/server retain that real production worker.

## Boundaries that are still explicit

- Cloning a source attached to original migration reference receipts still
  returns `PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED`. Site-reference and
  Transfer mappings need a reviewed common clone-binding implementation. Do not
  erase receipts, relabel migrated state as native, or reset user data to bypass
  this limitation.
- The audited version transition is CategoryEditor 1.2 to 1.3. Unknown historical
  versions and arbitrary business-owner schema migrations remain unsupported.
  This framework is not an external-repository installer or dynamic-code updater.
- Common instance coverage is not acceptance of every catalog plugin's entire
  business implementation. Unpublished independent plugin work is not included.
- Unprovided private-environment fixtures are not passed. Earlier NoticeV2,
  full App-test worker startup, nested-Form timing and shared payload-performance
  failures remain in their original records. Isolated later successes do not
  explain or erase an earlier intermittent observation.
- The new authenticated attempt/recovery evidence is not an established database
  downgrade path for older binaries.

These boundaries prevent describing this integration as every possible framework
or migration scenario being complete. They do not invalidate the explicitly
verified supported lifecycle and user-facing paths listed in its evidence.
