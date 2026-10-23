# Plugin instance projection: backend milestone

This adds a trusted, transaction-scoped host extension, not a completed plugin lifecycle or a production Merge Form implementation.

## Contracts

- `NormalizeInstanceDefaultsWithProjection` preserves the existing missing-only merge, exact instance/version/generation/config-revision checks, busy rejection, encrypted namespace, and pending-run proof propagation.
- Actual writes require the compiled `transactional/v1` declaration and a non-nil trusted host callback. A persisted manifest cannot grant this declaration. No production plugin declares it in this milestone.
- The callback receives copied configuration/manifest views and a transaction wrapper. Bound `Data` / `SaveData` operations address only that instance and retain encryption and revision checks. A callback or later propagation error rolls back configuration, business state, pending input, and proof edges together.
- The wrapper has no exposed Commit/Rollback methods. Its SQL methods are for trusted compiled host code, not an untrusted-plugin sandbox: transaction-control SQL, cross-instance writes, external I/O and irreversible side effects before commit are forbidden by this contract.
- A read-only `PluginProofReader` lets trusted owner validators inspect existing deferred/origin proofs in the same transaction without requiring `*sql.Tx` or acquiring commit authority. It does not grant execution or rewrite historical receipts.
- Read-only no-op requests do not execute the callback. Business adapters must independently authenticate their state before presenting it; a no-op is not an owner-state validation receipt.

## Evidence

- V6 projection source: ordinary and race each passed 16 top-level tests / 44 inclusive cases.
- Final reader source: ordinary and race each passed 26 top-level tests / 71 inclusive cases, with all 5,429 captured source files unchanged before/after. Exact source hashes and summaries are in the companion JSON.
- Coverage includes atomic sequential updates, two real compiled plugin manifests, cold-clone data isolation, callback and propagation rollback, compiled-declaration rejection, unchanged no-op behavior, and existing authenticated config-save origin/transition checks.
- The mechanical positive projection tests use the private transaction primitive with real SystemProcess and CategoryEditor manifests. They are not evidence that a production transactional owner is integrated. Public declaration tests explicitly reject currently undeclared owners.

## Boundaries

This does not publish frontend assets or merge into the aggregate branch. Real Merge business Form integration, lifecycle cold-state proof, reset/update/version migration, and full end-to-end framework acceptance remain separate work.

The existing defaults API deliberately remains missing-only. Original Merge Form can remove existing flags/empty-suffix keys and distinguish live Config from StoredConfig. That requires a separately reviewed trusted full-config transition, not silently widening this API.

The earlier full-App-race Uninstall form timeout remains in the verification record. One bounded instrumented rerun passed, but did not establish the historical cause. The later test-only layered fixture candidate is not included here. These focused successes do not turn the earlier complete race shard green.
