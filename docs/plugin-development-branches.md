# Plugin development branches and integration

The project uses fixed task branches for parallel development, following the updated development request on 2026-10-22. This replaces the earlier requirement to publish every intermediate milestone directly to the integration branch.

## Fixed branches

- `plugins/merge-message-notify`: MergeMessageNotify.
- `plugins/remove-link`: RemoveLink.
- `plugins/auto-sign-in`: AutoSignIn.
- `plugins/framework-lifecycle`: common plugin instance creation, namespace isolation, reset/update lifecycle and the developer contract.
- `dev/plugins-continuation`: serial integration of accepted plugin work.

All four task branches were created from `b4a33f6a0cb1c2387d8c948758da2b38a6f9b8c2`. Their remote refs were read back after creation. Each task keeps one fixed branch, an isolated source tree/index and its own exact validation evidence. Do not create a branch per test attempt.

## Publishing and acceptance

Verified milestones may be committed to the relevant task branch before the whole plugin is complete. Each milestone must distinguish passed checks, failures, skips, untested behavior and remaining implementation work. Registration, compilation or a targeted test alone does not establish plugin completion.

A plugin branch is integrated only after its plugin acceptance is complete. Integration is serial: inspect the current destination, combine the accepted changes, rerun affected regression gates on the exact combined bytes, and push without force. Do not overwrite unrelated work, delete branches, or reset another task's uncommitted files.

Common framework code has a single coordinated owner. Plugins consume the instance/lifecycle contract rather than adding their own clone/reset/update paths. Shared-file changes and validation resource usage are coordinated before integration. Generated frontend assets must be rebuilt from the combined frontend source, not copied over a newer integration build.

The separately accepted shared notification timezone correction (`d6c5ca6766d48d6b24fbc214fbefc9c619532401`) was authorized for direct integration. It does not merge any incomplete plugin task.

The current implementation and acceptance scope is Linux with local storage. Normal plugin network functions remain in scope. Production deployment, real accounts and destructive operations are not implied by development acceptance.
