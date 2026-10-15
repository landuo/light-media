# TMDB catalog

`New(Credentials)` is the application client, separate from the fixed M0 probe.
It searches movies/TV, browses rankings, returns details/cast/recommendations,
lists season episodes with their own source IDs, and exposes actor biographies
and combined acting/crew filmographies. All operations accept `context.Context`.

Details append `external_ids` and `alternative_titles`. Only a validated IMDb
title ID (`tt` plus 7–10 digits) and at most 16 normalized, deduplicated Chinese/
English release aliases (CN/TW/HK/SG/US/GB/CA/AU) are exposed. Supplied nested IDs
must agree with the verified parent. Social identifiers/URLs, control characters,
other regions and oversized alternative-title arrays are not search keywords.
Movie `titles` and TV `results` response shapes are handled independently. Search
cards do not claim these detail-only identities.

The only API origin is `https://api.themoviedb.org/3/`. Supply exactly one API key
or read-access token in memory. The client does not read files or environment
variables and does not persist, log, or JSON-marshal credentials. Redirects are
blocked, including same-origin redirects. Errors carry a fixed safe code/text;
provider bodies and transport causes never become error strings.

Chinese (`zh-CN`) is requested by default. Detail, person and season records use
the same record's English text only for missing localized fields. Search cards
fall back to the original title without issuing one extra request per card.
Fallback failures leave the valid primary metadata intact. Empty dates are
unknown, not inferred; TV `release_date` is its actual `first_air_date`. Seasons
zero (specials) are allowed. Episode IDs are never substituted with series IDs.

`Search`: kind `movie`, `tv`, `all` (or empty); page 1–500. Multi-search's actor
results are excluded from title cards; provider totals may include those people.
`Browse`: kinds `movie`/`tv`; lists `popular`, `top_rated`, `trending_day`,
`trending_week`; movie also `now_playing`/`upcoming`, TV also
`on_the_air`/`airing_today`. `all` is accepted only for trending. The API's usable
pagination limit is capped at 500. Filmographies are a single non-paginated page,
merged by kind and ID while preserving character and crew jobs.

Each client permits at most four in-flight requests and schedules at most four
new requests per second. Every operation request, including scheduling, has a
12-second deadline. A 429 reports `RetryAfter` and blocks further calls during a
bounded cooldown (up to 60 seconds); there are no hidden retries. Responses are
limited to 4 MiB and collection lengths, IDs, dates and relationships are checked.
Image fields remain TMDB relative image paths; external URLs are discarded.
The application should cache catalog responses and expose `Error.Code` to users.

Official references checked while implementing:

- [Application authentication](https://developer.themoviedb.org/docs/authentication-application)
- [Rate limiting](https://developer.themoviedb.org/docs/rate-limiting)
- [Multi search](https://developer.themoviedb.org/reference/search-multi)
- [Movie details](https://developer.themoviedb.org/reference/movie-details)
- [TV details](https://developer.themoviedb.org/reference/tv-series-details)
- [Season details](https://developer.themoviedb.org/reference/tv-season-details)
- [Person details](https://developer.themoviedb.org/reference/person-details)
- [Combined credits](https://developer.themoviedb.org/reference/person-combined-credits)
- [Trending movies](https://developer.themoviedb.org/reference/trending-movies)

`go test -race ./internal/catalog` uses synthetic credentials and local httptest
servers through a private transport seam; it makes no live TMDB calls.
