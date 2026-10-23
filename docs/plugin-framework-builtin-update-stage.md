# Built-in update: first executable migration stage

This is an incremental framework milestone, not a completed general plugin updater or an external plugin-repository installer.

## Implemented production path

The authenticated plugin update endpoint accepts a captured source version, target version, generation and configuration revision. The target must be the currently compiled implementation and the source-to-target conversion must have an explicit compiled declaration. Same-version requests are authenticated read-only no-ops: they do not cancel an active run, increment generation or enqueue another initialization.

For a real conversion the common lifecycle supervisor fences the precise instance, cancels and joins active work outside the SQL transaction, and rechecks the authenticated installation and revision before committing. The transaction updates the compiled manifest and generation, preserves authenticated clone presentation/lineage, encrypted configuration and KV data, retires old schedules/subscriptions and pending work, and queues target initialization only for an installation whose desired state was enabled. Unknown admission/commit outcomes are not reported as successful rollback or successful migration. This path does not call install/upsert or native owner genesis.

The response distinguishes committed migration from the later initialization run. A pending initialization is not a successful execution. No rollback promise is made for external effects performed by a later run.

## First supported declaration and source evidence

The first reviewed conversion is CategoryEditor 1.2 to **exactly 1.3**; it does not implicitly authorize a future compiled version. Both versions persist `enabled` and `content`; unknown configuration keys and original bytes are preserved.

Pinned upstream source:
- [Version 1.2](https://github.com/jxxghp/MoviePilot-Plugins/blob/69813a367dc0835cfaa92cbae49f2e2ac9a10f66/plugins/categoryeditor/__init__.py), SHA256 `646dd76d25e079468ca0b57ad6b36d0c4132c3add146745f1e45a45f608cd7c4`.
- [Version 1.3](https://github.com/jxxghp/MoviePilot-Plugins/blob/f3e5d857c4ffe81f916c15bf33eb67af2af00f33/plugins/categoryeditor/__init__.py), SHA256 `24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27`.

The older implementation leaves the one-shot `enabled` flag true after writing its file. The target clears it after execution. Therefore a legacy true flag is **not** treated as fresh authorization to repeat that external operation. This stage supports validated false/null configurations and rejects true flags without clearing them. Supporting enabled one-shot migrations safely remains unfinished; users should not have to infer completion from a hidden button. Unknown native-state conversions also remain unsupported.

## UI and development contract

Installed cards receive an executable update offer only when the compiled declaration and configuration satisfy the narrow transition. The actual write revalidates all conditions in its transaction. Unsupported conversions and configurations needing review show an explicit unavailable reason. Confirmation sends the captured precise instance snapshot, not a display-version string or a plugin-family identifier. Cancel does not write. Success wording distinguishes migration from initialization.

Plugin implementations add a reviewed business migration declaration and validation, not their own clone IDs, run-cancellation registry, update reinstall, reset orchestration or namespace deletion. Future native-state migration hooks need explicit atomic state projection before those conversions can be offered.

## Verification and limits

- Eight backend roots passed both ordinary and race runs on identical product Go bytes. Coverage includes exact revision/generation/target rejection, invalid/true one-shot rejection, same-version active-run no-op, rollback preserving old installation/config/KV bytes, formal clone isolation and authenticated HTTP dispatch.
- The actual ordinary `cmd/server --plugin-worker` target was exercised by both host modes. Safe migration did not write the synthetic category file; only a later authenticated explicit configuration save requesting execution caused the target to write and clear its flag.
- Full frontend suite: 215 passed, zero skipped; type check and production build passed. Initial SFC test-only failures (static Boolean attribute representation and selecting a hidden unrelated dialog) are retained; production code was unchanged by those fixture corrections.
- Vet for app/plugins/builtin and final actual server build passed. The first browser fixture omitted the actual server worker binding and therefore produced immediate Form errors for both source and updated instances; that failure is retained. The test-only fixture was corrected to verify and use the actual server binary, and its new compilation passed. The second real assistant-cloud Chrome run passed confirmation/cancel, blocked-reason display, double-click single submission, refresh persistence, updated Form rendering and configuration/presentation retention, and source/blocked-sibling isolation. Both owned browser services exited 0 and were reaped; the first run remains partial rather than being relabeled green. No CI execution is inferred from an empty check list.
- Only synthetic owned local data was reset or changed. No user media, production database, repository deletion, credentials or external plugin marketplace was involved.
- The earlier grouped NoticeV2 intermittent race failure and full App-test-race pre-request startup risk remain documented in previous milestones. A new update-stage pass does not explain or erase them.
