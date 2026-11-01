# Clean-baseline CN backend: independent actual final review

**ACCEPT — finite no-Meta CN backend and its directly affected consumer, normal and race.** This is not full S11, full Meta, normal-server CLI, public TMDB, or publication/commit acceptance.

## Exact candidate and protected baseline

Candidate `ce06bf49964b78c39c8d63883ebaaa1f650c4938fc57d8360b4f1d8968330ff3`, seal `172441d00e948b3673834c6e8df366a2ebd2ea9e63ab942fc8be17aaf40b9589`, base `db671b145270084bc769e433cd7c68952cb8c6d0`.

The 29-entry compiler overlay restores three tracked Meta hooks to exact base blobs, hides exactly 17 untracked Meta Go files using empty backing strings, and adds eight CN inputs plus one test-worker mapping. No original file was deleted or changed for the overlay. Hidden-file hashes remain protection inputs, **not compiled declarations**. The 49-file frozen candidate was independently checked again. Earlier static admission and Git/blob comparison remain in `../preexec01`.

Current-file verification was completed at **2026-11-01 06:12:32 UTC, before the separately authorized materialization**: all 6352 normal and 6361 race input hashes/lengths match all three runner snapshots. This statement is historical and must not be reused as a claim that later intentional source materialization left the old physical closure unchanged. Ledger is intentionally outside this immutable closure.

## Actual execution

| Gate | Tool/session | Inputs / declarations | Result |
|---|---|---:|---|
| normal01 | 44127d → edf84c, session 73860 | 6352 / 7810, 432 packages | exactly 3 roots PASS |
| race01 | 77a989 → c6c4e6, session 9061 | 6361 / 7819, 434 packages | exactly 3 roots PASS |

Normal ran 06:08:20.931419–06:08:48.088368 UTC; race ran 06:08:59.758398–06:09:44.129104 UTC. Go-list, worker build, App compile and test exits are zero. All four stderr files in each gate are empty; no failed or skipped test events. Original tool receipts are coordinator transcriptions, accurately labelled, not newly replayed tool records.

The exact roots were:
- `TestSourceTMDBProviderInfoCNSource05`
- `TestOriginalMediaRecognitionOwnedProviderCNSource05`
- `TestOriginalRecognitionNativeModelAndGroupBoundaries`

No owner-suggested fourth root or historical 80-case suite was run. The old direct consumer root covers retained non-CN model/group/error/recovery/config-guard behavior. It also creates an owned App for its guard checks; it is not described as entirely I/O-free.

Both go-list declaration sequences were independently reconstructed from the raw package records and agree exactly with the recorded virtual/backing declarations. No hidden Meta Go path occurs. All backing files and both generated test-main sources are bound. Normal App SHA `8acd74e2321a07f678aa355ede4ef8505f8e86c155259e5e5719d00923cb560b`, worker `dafb69c8f2282d015c01e52291c6ac43bf91e57fa75ecf6efe4b8dea12ce52cd`. Race App `c84577039beb608ad3fefa4f44c6be3edf1bbb45af0e95f9a13823bd627b5601`, worker `3f142f1fa5e63e4e5a2ba2e8b9c0d8212fce3edfdc664911cf2af246a365459e`. Race flags are present in go-list and **both** builds.

**Vet was explicitly NOT_RUN (`-vet=off`).** The prior virtual-directory vet failure remains recorded; neither this review nor the successful binary executions turn it into a successful vet gate.

## Source and actual owned-chain observations

The embedded source raw remains exact SHA `285ad4a28c5b5bc03533baff22ff1bb43da490fa27be99222d3de1aa2f86dffa`. The pure App root compares four complete original `get_info` dictionaries and one direct TypeError. The only dictionary representation mapping is the top-level source MediaType enum to its value. Source partial in-place mutation on error remains in the oracle; target caller-object mutation is not claimed.

Each owned root performs six actual installed SDK callbacks, five fixed owned provider GETs and two artifact GETs. Warm PIDs are 127 (normal) and 140 (race); cold PIDs are independently checked in the attached readback. Warm invocations 1–5 remain on the same worker: movie, TV, numeric-title source error → None, recovery, then cached movie without a fifth warm fetch. Cold reopens the same database, retains the installation identity, starts a new worker with init/invocation 1, does one new provider read and does not refetch artifacts.

All six complete response bodies were reconstructed from raw test2json output, hashed and independently parsed. Every non-null `tmdb_info` agrees with the complete corresponding original source output, including typed values, nested sequences and absent/null distinctions; selected identity/title/translations/name/genre projections also agree. Normal and race raw results are equal. Movie SHA `bb8773bcbbb228967824d3e6efab542af873bc91759a966d15eb0ffef0f336ee`, TV `4af7e7643f3b27fd4006912a34c4e31f9511263f7642ec370b8004272b640ab6`, null `74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b`.

These are complete observed target models, **not a newly collected whole-MediaInfo source oracle for these CN fixtures**. Whole provider dictionaries are independently compared; remaining model/category credit is inherited from the established component evidence plus this directly affected old root. Warm/cold complete-model equality is self-consistency evidence, not a substitute source oracle.

## Scope and safety boundaries

The installed path uses real App.New/Handler/initialization/Close, SDK processes and owned HTTP. It is not App.Start or an ordinary server CLI run. Constructor-injected transport accepts only the exact logical TMDB GETs and rewrites them directly to the fixed owned peer before any external request. No upstream, CONNECT, redirect, arbitrary forwarding or failure fallback exists. Peer counts and code guards support that bounded statement; no OS-wide network/syscall absence is claimed. Production TMDB origin/proxy routing is not tested by this injected transport.

The inherited center helper disables **PluginStatisticShare** and clears **PluginServerURL**. Earlier broad wording that all share/server settings were unchanged was corrected. MediaRecognitionReport Share/API and proxy/CA/environment were not changed to evade reporting. The executed clean no-Meta route contains no report call; the original complete report payload guard also returns None for Meta=None. The separately blocked with-Meta report gate is not repaired, retried or accepted here.

The approved subsequent same-byte 15+4 materialization, new-package converter tests, normal vet, publication inventory and commit are separate steps. This final evidence approves only the frozen overlay candidate and the six actual root executions above. No test/source/vendor/native execution was performed by this reviewer.
