# Trusted Form sessions: V10 backend milestone

This is an independently verified backend stage, not completion of the plugin lifecycle framework. It builds on the V8 full-config projection and V9 lifecycle evidence APIs. No frontend, external plugin repository, marketplace, update installer, or complete reset migration is added here.

## Production changes

- An authenticated HTTP Form request captures exact instance identity, lifecycle mode, durable configuration, display configuration and revision. It rechecks the captured anchor around acquisition without holding a SQL transaction or mutex across a worker call.
- Trusted host calls validate the anchor before invoking business code. Committed reviewed configuration projections advance the session only after commit. External edits, reset, claim or identity changes fail closed rather than returning an old model paired with a newer revision.
- Final HTTP configuration revisions come from the trusted session, not worker-supplied SnapshotRevision. Returned host failures are sticky and use a fixed safe error allowlist; arbitrary worker/business error text is not exposed.
- FormModelPolicy separately declares installed and preview model selection, with host_overlay as the compatible default and source as an explicit model policy, not permission to write. MergeSiteSwitch declares source/source; the former plugin-ID-specific response revision exception is removed.
- trustedPluginFormView returns detached copies of captured mode, installation and durable/display values. It is a request-bound read view, not a new transaction authorization or persistent owner proof.
- Reinstall preview is distinguished from installed Form handling. A POST install for an already installed instance returns PLUGIN_ALREADY_INSTALLED before invoking a preview worker. No update API is implied by this install restriction.

The original Runner time budgets, semaphore and Transfer observation remain unchanged. Valid presentation from an old same-generation/revision in-memory overlay is a point-in-time capture; this stage does not invent a cache epoch.

## Actual verification

Candidate3 contains 5,441 frozen files. The exact 54 top-level tests / 164 test nodes passed both ordinary and race execution, with no failures, skips, missing roots or changed frozen bytes. App race elapsed 121.653 seconds; protocol race elapsed 1.036 seconds. Tests cover real compiled Form handlers, concurrent drift, immutable views, canonical/redacted errors, no-op and committed/equal config saves, HTTP revision handling, lifecycle evidence and unchanged product failure diagnostics. Mechanical projection fixtures are not represented as a deployed MergeMessageNotify integration.

The earlier candidate remains a recorded failure: external configuration drift reached MergeSiteSwitch's business parser before final session validation, yielding generic PLUGIN_FORM_FAILED rather than PLUGIN_REVISION_CONFLICT. Candidate3 adds a pre-host-call anchor check and five new regression roots. Unknown business failures without preexisting drift remain generic. Drift occurring after this precheck is not claimed to reclassify every business error.

The accompanying JSON records exact source Git/SHA256 hashes, test roots, both final summaries and the first red summary. These are isolated V8 + V9 + V10 results, not a full suite on the published branch: the branch preserves four separately verified Uninstall process fixture files not present in this candidate. No unverified frontend bytes are included or overwritten.

## Remaining boundaries

A generic callback dispatcher for preview/cold/started/legacy-unknown modes is a separate stage, not this commit. Real MergeMessageNotify Form callbacks still require integration and fresh production-path tests. Existing native business state hooks remain trusted code; this is not a Go sandbox. Consistent rollback of all database evidence has no external monotonic protection. V7's large-config race case remains pending, and complete reset/update/version migration/live cancellation still require production wiring and end-to-end acceptance. Nothing here authorizes replaying unknown side effects or counting an unfinished plugin as complete.
