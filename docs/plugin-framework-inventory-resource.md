# Standard plugin framework: read-only inventory slice

The complete plugin framework is **not finished**. This development-branch slice adds one real, bounded host-resource operation, not a restored business plugin or a new lifecycle mechanism.

## Contract

A standard artifact declaring `light-media.sdk-resources/v1` and `brush.inventory.read` may call that method with `{}` during its actual authenticated SDK invocation. The host validates the current object, instance, plugin ID, version, manifest capability and cancellation scope. A declaration alone is not authorization.

The result contains schema_version 1, sites and downloaders. Sites retain exact int64 source_id values, including values above JavaScript's exact-number range; consumers must not decode them through float64. Disabled sites remain present. `enabled` does not mean connected, active, selectable, or absent. Downloader IDs and names are not interchangeable, and duplicate names are not resolved arbitrarily.

Site local statistics use the existing core view and its latest-row semantics, including error observations. `available` means a local row exists, not network success; missing values remain null. The operation does not contact sites/downloaders, allocate identities, create configuration defaults, or mutate statistics. Canonical origins and safe domains are projected without cookies, credentials, UA, RSS URLs, query secrets, proxy configuration, or downloader connection settings.

Configuration and identity publication share the existing manager lock. All local database reads use one read transaction, which ends before the final live-scope validation. No old plugin queue, revision admission, effect receipt, recovery workflow, or compatibility runtime is reintroduced.

## Verification boundaries

The owned executable fixture is an actual standard SDK artifact acquired through the artifact manager. Tests cover exact IDs, current invocation authority, cancellation/revocation, clone isolation, absent/missing local statistics, and no secret leakage or unexpected mutation. Ordinary and race runs build their own worker artifacts; the race parent uses race-instrumented worker binaries as well.

An earlier attempt packed the entire App test executable as the fixture and ended with `signal: killed`. Its cause was not established and is not relabeled as an OOM or a product fix. The smaller standalone fixture preserves the contract assertions and avoids that unnecessary whole-App child executable. Runner setup errors from omitted fixture environment and an incorrect SDK package selector were retained separately, not counted as product failures or passes.

## Still incomplete

This slice does not implement site/downloader network or write operations, external notifications, long-running/concurrent worker calls, post-callback scheduling, complete business event payloads, external messaging providers, generic Vue asset loading, or structured worker logs. Those require separate actual host/SDK/UI verification. Three paused business-plugin restoration candidates are not included. A previously observed unrelated UI subscription shutdown test has no confirmed root cause; a later full-run pass does not erase that intermittent evidence.
