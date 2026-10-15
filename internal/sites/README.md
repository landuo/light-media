# Native site adapters

The application package is independent of the earlier `integrations` M0 probes.

```go
client, err := sites.New(sites.Config{ID: "ubits", Kind: "ubits", BaseURL: "https://ubits.club/", Cookie: cookie})
result, err := client.Search(ctx, title, 1) // One-based page.
torrent, err := client.Torrent(ctx, result.Resources[0].ID)
```

Supported kinds: `mteam`, `hhanclub`, `ubits`, `jiayuan` (HDHome), `1ptba`. MTeam takes an explicitly configured API origin such as `https://api.m-team.cc/` and an API key. The other four use configured HTTPS origins and Cookie authentication. HTTP is accepted only for loopback protocol tests.

Nexus title searches use the M0-observed `search_area=0`; exact IMDb IDs use `search_area=4`. MTeam sends exact IMDb IDs as full IMDb URLs in `keyword`, matching MoviePilot’s linked official protocol; HHan uses its observed div grid. Its form input is named `keyword`, but verified GET searches use `search`; `keyword` in the URL silently returns the unfiltered listing. HTML parsing handles browser-inserted tbody nodes, nested title tables and seed/leech anchors. A changed template, unrecognized result row, malformed size/count, authentication page or ambiguous resource raises a fixed error instead of returning a false empty success. Valid rows before and after a malformed row are retained together with an explicit error so the application can report partial results. Empty results require an explicit marker inside the recognized result grid. The observed Nexus no-match page omits that grid, so it is accepted only on the same-origin torrents search endpoint with the authenticated logout signal, exactly one searchbox table and the exact observed message in a td.text/table chain. Nexus pagination follows only a same-origin matching next-page link; MTeam uses authoritative totals when returned. Missing MTeam seeder/leecher counts remain unknown (`-1`), never zero.

MTeam interprets per-torrent discounts, the download-only promotion rule and ongoing single-free entries in that order. Confirmed quality labels remain attached to the subtitle for policy evaluation; subtitles never create audio facts by themselves. Resolution, codec and source are title/description hints, and the decision module performs its own conservative quality checks.

Resources expose a reconstructed public detail URL with only the item ID. They never expose a passkey-bearing download URL. Torrent material is resolved server-side from a numeric resource ID. HHan re-reads its detail page to obtain the current same-origin ID/passkey link. Other Nexus adapters use their fixed ID endpoint. MTeam uses the fixed token endpoint and permits only the configured API origin plus the exact observed `fr1.halomt.com` CDN, with at most one validated redirect. MTeam material requests have no API key, Cookie, Authorization or Referer. HTTP redirects on authenticated HTML/API requests are blocked.

HTML/JSON bodies are limited to 8 MiB, material to 32 MiB. Material must pass `metainfo.ProductInspect` before it can be submitted to a downloader. HTTP logging uses the existing redacted trace transport; errors never contain URLs, response bodies or credentials. Concurrency, daily budgets, cancellation and workflow-level business logging belong to the application service.

Tests use synthetic fixtures and an in-memory HTTP transport, covering five adapters, promotions, labels, pagination, parser/auth failures, safe error reporting, material validation, origin restrictions, credential stripping and size limits. These tests do not claim a live site validation; the root integration run supplies that evidence separately.
