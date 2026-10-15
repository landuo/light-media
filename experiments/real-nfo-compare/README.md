# M0 real-source NFO association comparator

This independent, offline CLI compares supplied fixed-sample metadata evidence,
five actual NFO files, a `metadata-nfo` manifest, and scoped fnOS observations.
It makes no network requests, reads no credentials, writes no files, and does
not create/scan/delete a media library. It does not reuse the synthetic
comparator's empty Trim ID or actor expectations.

Run from the project root after the separately authorized producer and observer:

```sh
go run ./experiments/real-nfo-compare \
  --experiment-id <new-UUID> \
  --report work/<actual-metadata-report>.json \
  --manifest work/<metadata-nfo-generated-manifest>.json \
  --fixture-dir work/<five-NFO-root> \
  --observation work/<observe-result>.json \
  --file-evidence work/<file-evidence-object>.json
```

The manifest must be the `status=generated` output from `metadata-nfo --apply`.
Its input SHA256 must match the report's **original bytes**, not a reformatted
copy. The report must declare `live_request`, five requests, five unique fixed
samples and successful typed identities. Reports are declarations: the offline
CLI always outputs `input_source_authenticity=not_independently_verified`.
Retain the actual producer execution evidence separately; changing a synthetic
report's label cannot make it real. Synthetic test fixtures explicitly exercise
such labels without claiming provider contact.

Each manifest entry's type-scoped source key, parent key, date/year and season/
episode is checked against the report. Each actual NFO's size/SHA and XML must
match both the manifest and one of exactly two frozen encodings derived from
that report: **v1 uniqueid-only** or **v2 same-value tmdbid + uniqueid**. In v2,
`<tmdbid>` appears immediately before `<uniqueid>` (after `<aired>` for an
episode), and must equal that record's own SourceID. An episode's tmdbid is
never its parent ID. Duplicate/conflicting tmdbid, extra fields, alternate tag
spelling, reordered metadata and other formatting are refused even if the
manifest hash is recomputed. The v1 expected encoding is frozen inside this
comparator, so updates to the shared NFO encoder do not invalidate historical
v1 evidence. Both versions still require their actual original byte length and
SHA256 to match their own manifest. The
fixed output paths match `metadata-nfo`: movie/1602727, tv/1418 plus its S07E17,
and tv/206484 plus its S04E05. Episode uniqueids come from each episode's own
SourceID, never RequestedSourceID/ParentSourceID. Equal numbers in different
media-type namespaces remain different identities. Two episode records sharing
one typed episode identity are refused. Actors, images and video content are
outside this comparison and are not required or fabricated.

The observation must have `status=observed`, `action=observe`, the same UUID,
owned library GUID, unchanged library baseline, and exactly seven unique actual
nodes. Every detail must carry `scope_verified=true`, the owned ancestor, and a
present actual parent. Root Movie/TV parents are explicitly empty strings;
each Episode must point to its correct Season, and that Season to its correct
TV. The comparator does not supply missing parent/ancestor values. Titles/types,
S07E17/S04E05 and episode air dates must match. A returned episode release_date
must also match when present. For roots, the valid release_date's year must match
the NFO year; an optional year field must agree. A year-only NFO does not prove a
precise release date, so either a normalized January 1 or another date in that
year is accepted without claiming day-level verification.

`--file-evidence` is the `file_evidence` object extracted from the separate
`inspect_files` output, after that action has successfully read and validated the
actual file records. It has this shape:

```json
{
  "experiment_id": "<same UUID>",
  "owned_guid": "<same library GUID>",
  "root": "/vol2/1000/media/light-media-m0-test-<same UUID>",
  "bindings": [
    {
      "item_guid": "<actual Movie or Episode GUID>",
      "relative_path": "夜王 (2026)/夜王 (2026).mkv",
      "actual_path": "/vol2/1000/media/light-media-m0-test-<same UUID>/夜王 (2026)/夜王 (2026).mkv"
    }
  ]
}
```

The example shows one binding; exactly three are required, for the Movie and the
two Episodes. The other exact stems are `生活大爆炸/Season 7/生活大爆炸 S07E17.mkv`
and `诛仙/Season 4/诛仙 S04E05.mkv`. Item GUIDs and actual paths must come from
observed server fields. Do not fabricate this artifact from a local plan.
Missing file evidence cannot pass; different roots, item GUIDs, extra bindings,
traversal spelling and duplicate bindings are rejected. Comparison can check
internal consistency of a supplied file artifact, not independently authenticate
how it was obtained.

Exit 0 / `status=passed` means only
`contract=real_nfo_sample_association_v1`: source-report/NFO/scoped-item/file
association passed. `direct_standard_id_readback` is **separate**. It remains
`unverified` unless all five corresponding Movie/TV/Episode details explicitly
return matching `tmdb_id`, or `source=tmdb|themoviedb|tmdb_id` with matching `source_id`.
Values can be positive integer JSON numbers or canonical decimal strings.
The observer's `tmdb_id_state`, `source_id_state` and `source_state` sidebands
preserve fields that were explicitly returned but unsafe/invalid and therefore
omitted from the sanitized summary. Any `tmdb_id_state` is a conflict; a
`source_id_state` (or contradictory `source_state`) with an explicit recognized
TMDB source is also a conflict. These cannot silently become missing fields.
An unknown/non-TMDB `source_state` by itself does not assert a standard ID and
remains unverified. A separately valid `tmdb_id` may still prove its own
namespace, without comparing it to an opaque Trim value or another namespace.
Multiple explicit standard-ID assertions must agree; null, invalid values or
conflicts fail the field comparison. A bare source_id and Trim IDs (including
`tm...`/`tt...`) are not converted into standard IDs. Trim values are summarized
only as `opaque` or `empty`, never echoed. Some explicit standard IDs can pass
individually while the aggregate direct-readback status stays unverified.

Passed does not establish full M0 acceptance, complete provider ingestion,
service/cache isolation, cache reclamation or the identity of video payloads.
No cleanup is performed. No actor, image or optional metadata completeness is
required by this tool. Failures exit nonzero with fixed codes/check names;
paths, titles, response bodies and opaque IDs are not echoed. Local input files
are bounded to 2 MiB, regular and symlink-free; JSON duplicate/case-fold aliases,
trailing objects and excessive nesting are refused. Report, manifest and file
binding schemas reject unknown fields; observation extra diagnostic fields are
allowed but never emitted.

Validation:

```sh
go test -race ./experiments/real-nfo-compare
go vet ./experiments/real-nfo-compare
```

Tests are synthetic and cover wrong source/NFO ID despite recomputed manifest
hashes, parent ID substitution, cross-type equal numeric IDs, missing/incorrect
actual ancestry and parents, E17→E01, dates/years, file/library scope, ambiguous
JSON, symlinks, opaque IDs, explicit standard-ID conflicts, and separate
association/direct-readback/authenticity claims.

Profile regression tests cover frozen historical v1 bytes, v2 import input, conflicting/duplicate tmdbid, parent-as-episode substitution, conflicting uniqueid, and unsupported ordering/tags even with recomputed manifest hashes.
