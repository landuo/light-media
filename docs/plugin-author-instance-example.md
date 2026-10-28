# Plugin author guide: instance-aware business code

> Historical guide for the older compiled builtin framework. For a new installable Go executable plugin on the rebuilt platform, use [the current test-plugin quickstart](plugin-test-quickstart.md). Its SDK, local repository and package installation steps supersede this document's builtin registration instructions.

Current API reference: framework commit `e1fd711e`. This guide describes real
compiled APIs, not a dynamically loaded third-party plugin SDK. Linux and local
storage are the supported deployment scope. Normal network-backed business
operations are not prohibited by that storage scope.

## 1. Register business code, not a clone implementation

Use `internal/plugins/builtin/category_editor.go` and `system_process.go` as
working small examples, and register the compiled manifest/handler in
`internal/plugins/builtin/catalog.go`. A handler has the existing signature:

```go
func HandleCategoryEditor(ctx context.Context, method string,
    payload json.RawMessage, host plugins.HostCaller) (json.RawMessage, error)
```

The framework issues `plugins.Identity{PluginID, InstanceID, Version}` and the
instance-bound HostCaller. Read `plugins.Invocation.Config` for invocation
configuration; request only declared host capabilities. For example, the actual
CategoryEditor handler reads defaults using:

```go
raw, err := host(ctx, "category.read", json.RawMessage(`{"default":true}`))
```

The plugin does not choose a storage namespace, derive clone IDs from suffixes,
register a separate clone handler, or make an instance-specific global registry.
The compiled version selects code; a displayed clone version does not.
Configuration, values, runs, schedules and event ownership belong to the host's
instance context. A shared business resource is still shared: CategoryEditor
instances target the same category policy file, and SystemProcess instances
observe the same operating-system process list.

## 2. Forms and configuration

A plain SDK `config_form` returns its declared form/model without running business
initialization. For stateful host forms, the existing shared
`dispatchPluginForm(ctx, installation, pluginFormCallbacks{...})` selects trusted
Preview, Cold, Started or LegacyUnknown callbacks. Business callbacks only
project/validate their business state; they do not infer a mode from a missing
journal, start an owner, queue a run, or manufacture initialization evidence.
Preview and Cold are read-only. Started/LegacyUnknown must validate retained
state rather than silently replace it. Callbacks are synchronous and must retain
the provided context, not launch background work.

Changed form defaults require a compiled declaration. The actual declarations are:

```go
ConfigDefaults: plugins.ConfigDefaultsPure
// Or, for a reviewed same-transaction state projection:
ConfigDefaults: plugins.ConfigDefaultsTransactional
// Full source-compatible config changes have a separate permission:
ConfigFormProjection: plugins.ConfigFormProjectionTransactional
```

These are alternatives with different semantics, not flags to add indiscriminately.
Persisted manifest data cannot grant them. Pure defaults may only fill missing
keys; existing false, zero, empty and null values are preserved. A native owner
with independent configuration evidence cannot claim pure defaults merely to
make a form save succeed. The trusted host APIs are:

- `NormalizeInstanceDefaults(ctx, snapshot, expectedRevision, defaults)`.
- `NormalizeInstanceDefaultsWithProjection(ctx, snapshot, expectedRevision, defaults, projector)`.
- `ProjectInstanceConfig(ctx, snapshot, expectedRevision, desiredRaw, projector)`.

Use the exact observed installation/revision and return stale/busy failures.
Do not guess revisions or replace this with an unrestricted config write. The
projector receives only the real `PluginDefaultsTransaction` methods:

```go
Data(context.Context, string) (PluginStoredValue, error)
SaveData(context.Context, string, int64, any) (PluginStoredValue, error)
Config(context.Context) (PluginStoredValue, error)
VerifyInitialization(context.Context, PluginInitializationProof) error
```

No SQL, Store, Commit or Rollback is exposed. A projector changes only its
instance's business state inside the framework transaction, with no network or
irreversible effects. Failed operations remain failed even if the callback
swallows an error. See [typed facade examples](plugin-form-projection-facade.md).

Only a fresh authenticated configuration PUT establishes config-save origin.
Trusted host code can check `ConfigSaveOrigin(ctx, run)`; a worker-supplied
`config_saved` field is not authority. Form reads/default normalization, startup
reload and clone creation are not user saves. A one-shot action must obey its
reviewed source semantics and never infer fresh user intent from copied flags.

## 3. Stop, reset and durable effects

The plugin implements its necessary business stop/compensation behavior and
honors cancellation in host I/O. The common runtime owns exact run claims,
activation, process/deferred-owner lifetime, cancellation, join and release.
Plugins must not call private claim completion/release methods or copy execution
capabilities into a refreshed database snapshot.

Reset is a framework operation: fence the exact instance, cancel/join outside
SQL locks, verify identity/generation and durable evidence, then reset atomically.
A timeout or unknown side effect is not a successful stop/reset. Existing native
receipts remain the business source of truth; a business hook may validate them
but must not treat uncertainty as permission to replay. Other instances are not
cancelled or reset. No per-plugin Clone/Reset endpoint or cancellation registry
should be introduced.

## 4. Declare a reviewed builtin version transition

The real extension point is `builtin.UpdateMigrationFor`, currently containing
one audited transition, CategoryEditor 1.2 to 1.3. Its return declaration is:

```go
UpdateMigration{
    FromVersion: "1.2", ToVersion: "1.3",
    ValidateConfig: validateCategoryUpdateConfig,
    RequiresExplicitSave: categoryUpdateRequiresExplicitSave,
}
```

The named functions exist in `internal/plugins/builtin/update_migrations.go`.
They validate the target configuration and identify retained enabled intent;
they receive no lifecycle authority, SQL or network transport. This transition
retains configuration bytes and data; it is not a generic arbitrary owner-schema
migration engine. A new version needs an explicitly reviewed declaration and
real source-comparison/rollback tests, not a fabricated compatibility claim.

The host owns same-version no-op, exact compiled target identity, fencing/join,
transactional migration and review gating. Retained enabled/one-shot intent is
preserved but not executed by migration. A held uninstalled instance can be
restored cold using the UI's “恢复为待确认”; only an explicit save of that exact
instance/revision activates it. Do not call host-private restore permits from
business handlers. Unknown versions and invalid metadata are explicitly rejected.

## 5. Required tests and current boundaries

For a business addition, test the same handler with a default instance and a real
framework-created clone; separate configuration/history/schedules/cursors, source
unchanged, no cloned execution receipts, cold reopen, concurrent/stale saves,
request cancellation, reset during work, and rollback/unknown outcomes. For an
update, verify the exact from/to version, preserved intent/data, failure rollback,
repeat/no-op behavior, cold restore, and explicit-save activation. Use ordinary
and race tests plus actual cmd/server worker and browser paths when UI changes.
Never substitute compilation or a mock for the required real behavior.

The 21 compiled catalog entries have common instance creation coverage; this is
not acceptance of every plugin's entire business implementation. The user chose
fresh use with no old-system data migration. MoviePilot configuration/history/
reference imports and clones of imported reference-bound instances are outside
this task, not required follow-up work. Their existing
`PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED` guard remains; do not erase old
receipts or relabel imported bindings to bypass it. Ordinary native clones use
the delivered shared framework. Internal plugin version/data-structure updates
still require the exact compiled declarations above; that contract is separate
from importing data from another product.

See [reviewed update/restoration acceptance](plugin-framework-reviewed-update-and-restore.md)
and [runtime reset contract](plugin-framework-runtime-reset-stage.md). Historical
milestone documents describe their original scope; later explicit contracts
supersede them. Unprovided private-environment tests and retained intermittent
observations are not counted as passed.
