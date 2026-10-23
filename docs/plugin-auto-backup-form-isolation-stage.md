# AutoBackup fresh-form isolation stage

This is a verified important stage, not final plugin delivery.

The host now keeps the derived backup directory out of Form defaults. Empty `back_path` means the current instance’s private directory; its effective location is shown as a placeholder and hint. The original pure Form and common Clone mechanism are unchanged. Explicit user paths, even exactly equal to the default directory, remain explicit and are never rewritten.

A real regression first reproduced fresh install/init → save the displayed complete model → official Clone retaining the source directory and failing the clone write. The minimal host projection passes three new ordinary roots including an actual cmd/server worker and independent SQLite/secret restore, plus all three old path-isolation roots. The first restore fixture run lacked the reusable helper’s synthetic seed; that failure is retained, the fixture was corrected, and the existing restore assertions were not weakened.

CGO-disabled Linux build, vet and browser-fixture build pass. Exact shared renderer v4 (233 web tests, typecheck and bundle pass) closes VSubheader and six literal-show conditions. Real cloud Chrome verified draft hide/show retention, cancel discard, successful full-model save, formal Clone, clone-private default hint, inherited hidden values, local once execution and cleared once flag. Unknown effects still block config writes; explicit abandon preserves the source archive and creates one recovery proof with zero retries. Source archive bytes remained unchanged after the clone wrote its own archive. Test service exited 0 and was reaped; browser tab closed.

Pending: six focused race roots for this new Form delta, final dedicated-branch completion, then serial aggregate publication. The earlier 42-root race is not represented as covering this new code. No remote WebDAV, external notification, user backup or user credential was used.
