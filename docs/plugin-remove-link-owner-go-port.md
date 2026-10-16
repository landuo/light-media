# RemoveLink 2.17 continuous App owner

This slice connects a trusted native owner to the existing plugin run queue; it
is not a second job system. Registration, HTTP/runtime and terminal deferred RPC are now connected in the
compiled catalog. Production deployment and original data/config migration remain
pending; the private outbox still has no delivery consumer.

The SDK may request only a terminal `cleanup.configure {}` or
`cleanup.apply {event_id}`. The Runner must exit successfully with the exact
private handshake, release its process slot, and then enter the actual owner via
a sealed `started` execution. Worker JSON supplies neither configuration nor
filesystem authority. The owner checks the real installation, version,
generation, revision, persisted running run and actual input again.

Administrative calls wait for sorted per-instance gates and pause owners before
acquiring lifecycle. Guards take lifecycle, briefly snapshot plugin state under
pluginMu, release pluginMu, then acquire download and import locks. No caller may
wait for an owner while holding lifecycle or a process slot. Every filesystem
operation opens the private Effects lease only under that Guard. Mandatory
AfterAction reconciles real confirmed deletions before event/lifecycle success.
Retirement is control-only after the old-generation drain; it cannot reopen an
old file Effects lease after SQL advances generation.

`prepareRemoveLinkTransition(s)` binds the old scope. `publishTx` verifies the
new core scope and the real pending/running run and publishes the transition in
the caller's config/run SQLite transaction. `resolve(false)` rereads that marker;
a published unknown commit stays paused and returns
`PLUGIN_TRANSITION_UNCERTAIN`. It never guesses rollback. Explicit retry retains
RunID/Input and is allowed only before a deferred operation has started, with
current To and no unknown journal outcome. Explicit reload still stops using old
flags and creates the new machine; ordinary config init reuses the old machine
and applies new flags before its source Stop drain.

Root authority is derived from actual trusted monitor and local STRM paths.
Media roots retain their core IDs. A watch inside a media root reuses that
capability; other actual source directories must be within AllowedRoots. A broad
allowed parent is not itself a source capability. Retained sealed capabilities
are reauthorized at restart and before every action. The new `ExpandRoots` host
method appends disjoint roots only for the exact published ticket and running
RunID, durably before switching local operations. Existing paths/IDs/stamps and
order, queued work, deadline, pins and captured-event proof are retained. Unknown
publication cannot resume. Broadening a source watch to an ancestor overlapping
an existing capability fails explicitly; unsupported cloud storage does not fall
back to local storage. A disabled install with no roots uses an inert journal and
no fabricated root/observer. Saving configuration after reset from revision zero
remains supported.

Cold configure also receives a real RunID-bound transition and lifecycle receipt.
Startup recovery restores enabled clean owners and their deadlines without a new
source Init; `shouldInitializeRemoveLink` suppresses that redundant startup run.
Readonly recovery may finish a previously started SDK operation only from its
exact sealed lifecycle/event completion and Core file-index receipts. Unknown,
blocked or interrupted work stays unexecuted pending reconciliation. No old
receipt is used as authority to run a newer instance.

The new owner tests use actual SDK child processes, temporary SQLite, owned
imported hardlinks, mandatory Core reconciliation, ordinary save/unknown commit,
root expansion, reset/re-save and a Linux production native watcher. Common
non-Linux tests inject only the watcher source. No real media, downloader or
external notification is accessed; private outbox pending is not delivery.

Earlier candidate/native-host NAS records remain historical evidence for their
original source hashes. They are not validation of this new owner/ExpandRoots
slice. Formal final App merged targets passed (15.388s), full App normal passed (85.267s),
App RemoveLink/Deferred/Uninstall race passed (126.917s), selected host
ExpandRoots/AfterAction race passed (4.101s), and vet for App/host/builtin exited0.
Full host normal passed (14.084s). NAS current binaries passed61 App targets and44
Linux host targets with no failures/skips/OOM, matching local/remote SHA and no
service state/start/restart changes. See plugin-remove-link-owner-nas-verification.json,
plugin-remove-link-owner-host-nas-verification.json and plugin-deferred-owner-execution.md.
These results include real SDK, native Linux inotify and owned filesystem/SQL proof,
not real users' media, seeds, message delivery, migration or deployment.

Post-first-pass additions preserve two different restart cases: an actual clean
Close runs the original immediate Stop drain, while an interrupted trusted
checkpoint restored through Suspend/Open/Recover preserves its exact queue
and deadline without source Init. A real SDK/started operation followed by a
synthetic deferred-result SQL failure proves that lifecycle/Core receipts finish
the original run after Close/New without opening an owner or deleting a new
same-path replacement. Lazy reload with falsy `{}` resets the business config
through a fresh source object, whereas ordinary init with `{}` retains it.

A readonly `prepared.validateConfig` preflight checks actual monitor/STRM
locations and storage support before config/run publication. Missing or
unauthorized directories fail without changing core revision; authoritative
Guard checks still repeat after publication to cover later changes.

The media extension policy was reread from the installed MoviePilot instance,
extracting only `settings.RMT_MEDIAEXT` and the config source SHA-256. The public
list is frozen in `installed-media-extensions.json` (source hash
`f19111073a7ef25ecd0b7e9d53dab343d76fe64ff3ad821f4ff39a790c104033`).
It has 18 ordered entries including `.3gp`, `.asf`, `.strm`, `.tp` and `.f4v`;
no unsupported `.img`, `.vob` or `.rm` entries are guessed. A reference regression
checks exact equality and mutation isolation. No other runtime settings or
credentials were read or retained.
