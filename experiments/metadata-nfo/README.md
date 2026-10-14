# M0 supplied metadata report → canonical NFO

Local-only Go experiment. stdin is one `internal/metadata.Report` JSON object,
at most 1 MiB; the CLI's optional `run_id`, `trace_target_id` and known `logging`
summary fields are accepted. Unknown fields (including provider hosts,
credentials, custom paths/settings or per-result evidence), noncanonical key
spellings, duplicate JSON keys, trailing objects and inconsistent samples are
rejected with fixed error codes. Keys must use lowercase ASCII report tags;
case-folded struct aliases cannot override a preceding status/identity field.
This tool does not call `metadata.Run` or any API, read NAS data, generate video,
images or actor records, create a media library, or scan/modify any service.

Run from the project root; the default is a plan with no filesystem writes:

```sh
go run ./experiments/metadata-nfo < work/metadata-verified.json
go run ./experiments/metadata-nfo --apply --out-dir work/metadata-nfo-new-UUID < work/metadata-verified.json
go test -race ./experiments/metadata-nfo
```

`--apply` requires an explicit, absent direct child of the local project's
`work` directory. The parent must exist without symlink components. The new root
is created exclusively with mode 0700; only fixed canonical paths are opened
through `os.Root`, with O_EXCL and mode 0600. Existing files/directories/symlinks,
external paths and nested output roots are refused. Neither the input filename
nor the output's absolute path is echoed. All five samples are validated and all
five NFOs encoded before the output root is created. Validation failure,
including the real unconfigured zero-request report, creates zero files. A
filesystem error during apply may leave this tool's newly created partial root;
it is reported as failure and is never silently reused or overwritten.

The report must declare status `ok`, exactly five requests and these five unique
samples, each status `ok`, source `tmdb` and no failure reason:

| Sample | Required identity/context | Fixed NFO path |
| --- | --- | --- |
| movie-nightking | movie/1602727, 夜王, year 2026 | 夜王 (2026)/夜王 (2026).nfo |
| series-bigbang | tv/1418, 生活大爆炸 | 生活大爆炸/tvshow.nfo |
| episode-bigbang | independent typed episode SourceID, parent TV1418, S07E17 | 生活大爆炸/Season 7/生活大爆炸 S07E17.nfo |
| series-zhuxian | tv/206484, 诛仙 | 诛仙/tvshow.nfo |
| episode-zhuxian | independent typed episode SourceID, parent TV206484, S04E05 | 诛仙/Season 4/诛仙 S04E05.nfo |

Every date must be a valid YYYY-MM-DD and match the report's year. Episode
`ParentContext` must be `verified_series_and_fixed_request_route` or
`verified_response_show_id_and_request_route`. The former explicitly relies on
the verified show plus fixed request route; it does not assert a response
show_id was present. Episode titles are supplied validated text, not inferred
from a filename. Show title and season/episode derive from the fixed context.
Movie and tvshow roots encode only title, year and uniqueid; their full source
dates remain in plan.Date as input evidence, not an NFO precise-release-date
claim. Only episodedetails encodes the supplied air date in `<aired>` along with
its fixed season/episode and show title. No unverified root date tags are added.

The NFO uniqueid always uses that result's **own SourceID**. Episode
RequestedSourceID/ParentSourceID identify the show route and are never copied
into its uniqueid. Identities are namespaced as `tmdb:movie:X`, `tmdb:tv:Y`, and
`tmdb:episode:Z`. Equal numbers in different type namespaces are accepted and
remain separate; duplicate identities within a type are refused. Tests use
explicit synthetic episode IDs/dates and exercise this distinction.

Accepted report evidence declarations are `live_request` and
`synthetic_http_roundtrip`; the plan preserves the supplied label and identifies
synthetic input. Both return `validation=supplied_validated_report`,
`input_source_authenticity=not_independently_verified`, and
`fnos_readback=unverified`. A self-declared JSON label, request count, run ID or
logging summary cannot authenticate a remote response. The input SHA256 and
each NFO SHA256 support later correlation with separately retained producer
evidence. They do not prove source authenticity, media/video identity, NFO
read-back, shared-cache isolation or end-to-end M0 success. No live report was
fetched or fabricated by implementing this tool.
