# AutoBackup 2.1.4: native Linux/local acceptance

This continuation starts from aggregate commit
`2f4ab1e5333501d890c2c803d11cbe0d32487329`. It does not reimplement the
original plugin or import MoviePilot data. Current requested scope is a fresh
installation and native configuration on Linux, using local storage only.
PostgreSQL, remote-storage providers, historical-data import and deployment to a
user NAS are not acceptance gates. Their historical records remain unchanged.

## Existing implementation and original behavior

The source reference is AutoBackup 2.1.4 with original source SHA-256
`dedab53b1adf96dc59879ebdd67f77fe67ea773981d4befb04ca000889f607e4`.
The port's immutable reference corpus and provenance tests remain in place.
The actual host already implements local ZIP creation from a consistent SQLite
snapshot, the matching restoration key and category policy, source retention,
once/cron and API dispatch, authenticated effect receipts, uncertainty blocking,
explicit abandonment and source notification history/routing. The earlier
“host still to implement” section in the pure-component report is historical;
the later host report supersedes it.

## Changes in this continuation

- The fallback archive directory, form default and staging directory now use the
  authenticated framework InstanceID. The default instance retains exactly
  `plugin-private/AutoBackup/backups`; an ordinary native clone receives its own
  corresponding private directory. No clone registry or lifecycle implementation
  is duplicated in the business plugin.
- Explicit `back_path` configuration is not rewritten. A user-selected shared
  destination remains shared and retains source overwrite/retention semantics;
  it is not covered by the implicit-private-directory isolation guarantee.
  Existing path grants still apply, including to a path copied by clone creation.
- Authenticated missing-owner evidence remains visible after the common attempt
  recovery pass. A mutable run error label is not accepted as evidence, and no
  unknown outcome becomes retryable or successful.
- Cancellation observed before a new effect intent starts is a known failure,
  not a new uncertain effect. Existing uncertainty is never cleared. Cancellation
  after an actual effect but before its receipt stays unknown and blocks Reset.
- Snapshot-cut and explicitly abandoned runs keep their authenticated canceled
  state after cold startup. This extends the immutable attempt record with a
  native recovery attestation, not a new execution permit. Explicit abandonment
  creates the proof in its own transaction so immediate Reset does not require
  a reboot; a proof-write failure rolls the abandonment back.

## Verification record

The machine-readable acceptance record distinguishes old baseline failures,
focused regression results, race execution, actual ordinary `cmd/server` worker,
independent restored App, and cloud-browser checks. The browser fixture and every
archive/database/credential in this run are owned synthetic test material.
No real external notification or user backup was read or sent.

Prior NAS/remote-provider tests are historical evidence only, not new runs.
Compilation, opt-in fixtures without their environment, skipped tests and failed
runs are never counted as execution passes. Local test success is not final
remote delivery; the fixed plugin branch and aggregate publication are recorded
separately.

## Verified stage, 2026-10-23

- Exact-baseline ordinary run preserved four failing roots (18 failing nodes);
  the formal-clone red test separately reproduced shared-default-path failure.
- Current App ordinary and race each pass 42 roots / 104 nodes, zero failures
  and zero skips. This includes five common-classifier neighboring roots.
- Pure Linux/local ordinary and race each pass 11 roots / 102 nodes, zero
  failures and zero skips. The ordinary pure source bytes remain unchanged.
- App race completed with a 14-minute test budget; build plus execution took
  942.45 seconds. The actual ordinary server test also verifies an independently
  opened restored SQLite App and exactly its own authenticated snapshot-cut.
- All 5,717 materialized source-file hashes were unchanged during final runs.
  Four unrelated remote historical binary blobs remain preserved by base tree.

This is a verified intermediate stage, not final plugin delivery. Final Linux
production build, vet, current cloud-browser acceptance and final publication
remain open. The compressed evidence bundle includes both failures and passes;
individual execution plans and source hashes make their scopes distinguishable.

## Build and partial browser verification, 2026-10-23

Production Linux CGO=0 / modernconly build, affected-package vet and the
opt-in browser fixture build all completed with exit 0 on unchanged source.
The real cloud Chrome session used the resulting production worker and owned
synthetic account/storage. Native Clone succeeded. Dismissing unknown-archive
abandonment left every observed business row and archive unchanged. Explicit
abandonment produced AUTO_BACKUP_ABANDONED_UNKNOWN, one durable recovery
attestation and zero retry records, preserving the archive and clone. Immediate
UI Reset advanced only the default instance generation from 1 to 2 and preserved
the archive and all clone rows. The owned fixture exited 0 and was reaped.

A real remaining blocker was found, not waived: the common form renderer rejects
VSubheader and the original boolean show binding {{webdav_enabled}}, leaving
AutoBackup configuration unavailable and Save disabled. Formal-clone Form JSON
and a screenshot preserve this failure. The shared renderer owner is repairing
these explicit source behaviors; a fresh real configuration-save/run browser
check remains required. No API save is substituted for this UI gate.

The first sandbox-loopback browser connection failed; it is not counted as a UI
pass. A separate official cloud-native Terminal launch supplied the reachable
owned service. No authentication bypass was introduced: the normal login form
used only the existing public test constants. Remote CI has no recorded checks
or statuses and is not claimed as passed.
