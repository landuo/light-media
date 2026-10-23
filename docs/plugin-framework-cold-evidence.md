# Authenticated plugin lifecycle observations: V9 milestone

This is an independently validated backend stage on `plugins/framework-lifecycle`, not complete plugin-framework acceptance or an aggregate-branch release.

## Behavior

The encrypted installation manifest carries a host-only lifecycle observation bound to the instance, implementation, version, generation, and distinct purpose. Normal install, reinstall, and cold clone creation create fresh cold evidence rather than copying source authority. Generic execution claim and all three private fast claim paths advance the observation to started in the same transaction as pending-to-running. Any action counts as started; this does not prove successful initialization.

All audited generation writers, including central reset/uninstall, PluginUnInstall target removal, and dedicated RemoveLink/CleanInvalidSeed/ScrapeFileClean lifecycle transactions, advance the observation atomically. A failed transaction leaves the old generation and observation intact. No plugin-specific clone branches are added.

The private Form-mode resolver reads actual database identity and clone metadata. It distinguishes absent or authenticated uninstalled preview, installed cold, started, and legacy unknown. Caller-provided manifest fields and missing business-owner state are not authority to call an instance cold. Present malformed or cross-bound evidence fails closed. The resolver itself does not load, initialize, or create a business owner and does not write configuration.

## Verification

The exact same 5,432-file isolated source inventory passed 20 roots / 52 inclusive test nodes in ordinary mode and again under `-race`, with no failures, skips, missing roots, extra roots, or changed source bytes. Ordinary package duration was 4.857 seconds; race package duration was 51.858 seconds. Tests include all claim paths, six dedicated HTTP generation transitions with rollback/commit checks, cross-instance ciphertext rejection, reset/reinstall/clone-of-clone behavior, stale same-reader admission, and the explicitly unsupported consistent-snapshot rollback case. Product budgets and assertions were not relaxed.

The isolated candidate is based on the previously verified V6-reader backend, not the later V8 Form-projection candidate. The 13 changed-path preimages independently match the current V8 publication parent. This stage does not claim a newly run full combined application suite. The separately developing V10 Form-session integration retained a first ordinary red result for an external-edit error-classification path; those V10 bytes are not part of this publication.

## Limits and remaining work

This observation is authenticated state, not a monotonic ledger. Replaying older valid ciphertext with the same binding, including restoring a consistent old database snapshot, can restore an older observation. A focused test documents that boundary. The evidence is not proof of business initialization, successful execution, compensation, or an external side effect.

Legacy installations without evidence remain unknown; they do not silently become cold. Preview/cold business forms must remain read-only until a separately authorized actual save/init path. Trusted HTTP Form sessions, production business adapters, common active-context cancellation, version migration/update rollback boundaries, and complete reset/update acceptance remain separate work. No new frontend build, external plugin repository, production-data deletion, or aggregate-branch merge is included.

Exact changed-file hashes, frozen input hashes, and terminal results are recorded in `plugin-framework-cold-evidence-verification.json`.
