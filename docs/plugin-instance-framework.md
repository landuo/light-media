# Plugin instance framework: contract and implementation milestones

This document describes the shared instance work based on release `4e50b304`.
A described contract is not a statement that every lifecycle milestone has passed
acceptance. The clone milestone and subsequent lifecycle work must have separate
recorded verification and publication results.

## Identity and plugin author rules

- `plugin_id` and the compiled manifest's `version` identify executable code.
  `instance_id` identifies configuration, values, runs, schedules, event cursors,
  native-owner journals and user-facing instance operations. Never infer one
  identity by splitting a suffix or parsing a displayed name.
- Workers receive the host-issued `plugins.Identity` and use existing scoped host
  APIs. They must not choose another instance's namespace, discover clones by
  suffix, or make global mutable singleton state for instance-owned values.
- Clones share compiled code. They do not duplicate a Go plugin source directory,
  change its manifest identity, download executable code, or introduce a plugin
  repository. A custom displayed version is presentation only.
- A plugin owns its business settings, validation, events and necessary hooks.
  The host owns creation, staging, namespace assignment, conflict checks, lifecycle
  generations and schedule ownership. This is Linux/local-storage development;
  regular network-backed business operations remain in scope.
- Different instances may intentionally operate on the same global business
  resource (for example the category file). Namespaced config/history is not an
  assertion that this global resource was duplicated or made conflict-free.

## Clone milestone

`POST /api/plugins/{source_instance_id}/clone` accepts `suffix`, `name`,
`description`, optional `version` (display only), and optional `icon`. The suffix
is 1–20 ASCII letters or digits. The new identifier is the complete
source instance identifier followed by the lowercased suffix, subject to the
existing 96-character host identifier bound. The server returns the created
identity; clients must not assume that a display name is an API identity.

Creation is a single storage transaction. It rejects live and uninstalled
identifiers, validates the exact available implementation, uses the existing
installation hooks for fresh owner roots and source membership, decrypts the
source config and re-encrypts it in the new namespace, and appends the clone to
the source’s existing folder while retaining unknown folder fields. A
failure at any step rolls back the entire clone. A displayed name/version/icon
and source lineage are authenticated and encrypted separately from the manifest.

Only configuration is copied. KV history, executions, results, scheduled work,
event subscriptions and native-owner journals are not copied. Existing `enable`
and `enabled` config fields become false. Missing spellings are not invented:
strictly typed plugins must not need a clone-specific unknown-field exception.
The host stages the instance regardless of those flags and does not queue `init`
or other business execution. Explicit configuration save activates the ordinary
framework init path. A form read may invoke the normal read-only worker form API.

Existing plugin-ID compatibility references do not silently start selecting newly
registered clones. An authenticated clone lineage excludes it from those legacy
references, even when the source is later uninstalled. Unidentified legacy
multiple-instance mappings still fail as ambiguous; an instance name or a suffix
is not a migration receipt.

### Current explicit boundaries

- The first clone milestone accepts native reference namespaces. Sources still
  bound to dedicated original-migration reference receipts are rejected with
  `PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED`, rather than copying old identifiers
  and falsely declaring them native. Original execution handoff gates are not
  treated as satisfied by disabled flags. Generic transferable reference bindings
  belong to the next lifecycle/reference milestone.
- Full uniform reset, update/version migration, staged failure rollback and
  explicit inter-instance reference selection are subsequent work, not claimed
  complete by the creation endpoint.
- Existing per-plugin lifecycle owner barriers remain intact. Creation reads a
  consistent source snapshot but does not pause, reset, abandon or rewrite the
  source's outstanding execution evidence.
- A removed clone identifier remains reserved by its installation tombstone.
  It cannot accidentally resurrect old namespace data through clone creation.

## Actual MoviePilot comparison

Pinned original source: `jxxghp/MoviePilot` commit
`8a83efeacd3e26b82be495e17d50146e31c96a60`, `app/core/plugin.py` lines 1905–2138,
blob `8fc8954d7e7df32b970f16f461efa244fa2f142c`.

The original manager centrally copies the Python directory, generates
`plugin_id + suffix.lower()`, changes class/config-prefix/frontend resource names,
updates display metadata and the installed list, copies config with both
`enable` and `enabled` false, then reloads and calls init again. Therefore the
new host's cold creation and immutable implementation identity are deliberate
safety/architecture improvements, not a claim that the Python original already
provided this exact process-boundary instance abstraction.

Original reset (`app/api/endpoints/plugin.py` lines 739–760, blob
`9dfe576f6157afa5aae0226963ac74098e44e7c7`) emits `PluginDataReset`, stops the plugin,
deletes configuration and KV through the manager, and reloads. The ordering is
framework-owned; plugin compensation and stop handlers remain business hooks.
The common framework must preserve such necessary hooks, not replace a real
business reset with an unconditional table delete or assume external effects can
always roll back.

The original UI restricts suffixes to 1–20 alphanumeric characters and permits
empty name/description overrides (which keep original metadata); the host uses
that same rule. The original folder API appends the new ID at the end. These were
checked in pinned frontend `2829e3b08214348f05ff2f5beae644882eccad58`,
`src/components/dialog/PluginCloneDialog.vue` (blob
`d3917049a833ac1d616299a68bdcf3b37b361ddd`), and backend `plugin.py:1063–1071`.

Original `MergeSiteSwitch` uses fixed original instance IDs, not all instances
sharing executable code: repository reference
`testdata/plugins/merge-site-switch-typed/reference/__init__.py` lines 41–53,
590–630, 768–803 and 833–844, SHA256
`52d798421eabe57a88f3d0fc448289309fdfc8ecd6ea50d98cd5436d93289ab0`.

Original clone uninstall additionally deletes the clone's config/KV and Python
code directory, unlike normal uninstall. This milestone keeps the existing Go
host's retention behavior for uninstall; it does not claim identical clone
uninstall semantics or permission to remove production data. Full lifecycle
policy and hook-based synthetic-data acceptance remain separate work.

Original update is not fully transactional: its UI passes `force:true`; the
installer backs up only when `force_install` is false, and reload happens after
installation outside its directory rollback flow. Hook failures may be logged
without making the endpoint fail. A new update framework must describe stronger
staging/rollback guarantees as its own behavior, not imply the original already
rolled back all config, scheduler state or external effects. Sources: pinned
backend `app/helper/plugin.py:2805–2840`, endpoint `plugin.py:603,626–628`, and
pinned frontend `src/components/cards/PluginCard.vue:232–235`.

A reproducible original-method capture is stored under
`testdata/plugins/instance-framework/`. It executes the exact pinned original
`clone_plugin` AST with synthetic config/registration/code-rewrite dependencies
and real stdlib filesystem copy only inside an owned temporary directory. It is
not a claim to launch the whole MoviePilot service. The oracle records ID
normalization, flag/unknown-field copying, absent configuration, duplicate
rejection and owned-directory cleanup on rewrite failure. Go tests compare the
shared contract and explicitly assert the two intentional deltas: no implicit
initialization and no invented extra enable-field spelling for typed plugins.
