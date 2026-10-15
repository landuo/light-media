# Resource decisions

`Evaluate(resource, Target, Policy)` is an offline decision. It performs no
network request, downloads nothing and never derives an episode from a calendar.

The selected catalog identity supplies exact Chinese and original titles. Case,
punctuation, separators and up to two bracketed release-group prefixes normalize
for comparison. A sequel or a title containing only the selected title as a
substring does not match. Both aliases together are allowed. Movie release years
must match for automation; absent/conflicting years require manual confirmation.

TV numbers go through `media.Parse` and `media.Resolve`. Absolute numbers require
a confirmed mapping scoped to this `tv:ID`, site/form and optional release group.
Known episodes are authoritative metadata; a schedule or early-release permission
cannot add episodes outside that set. Auto acceptance requires an explicit wanted
set, all resource episodes inside it, and air evidence or explicit permission for
each episode. Packs, unresolved numbers and extra episodes require confirmation.

`Accepted=false` is a hard policy/identity rejection or an automatic candidate
that still needs confirmation. With `Auto=false`, a confirmation candidate is
previewable as `Accepted=true, NeedsConfirmation=true`; the application must show
its reasons and require confirmation before adding it. It must not discard that
flag when creating a download preview.

`MinSeeders:0` explicitly allows zero seeders. An adapter must encode an unknown
count as `-1`, which is rejected. Free-only requires both the explicit free flag
and zero download factor, and rejects expired or malformed known promotion end
dates. A missing free flag never becomes free merely because a numeric field is
its Go zero value. Resolution/codec, known size bounds and optional RE2 include /
exclude expressions are enforced; invalid/overlong expressions fail closed.

`Source` requires explicit WEB-DL / WEBRip / BluRay / BDRip / HDTV / DVD / REMUX
evidence. REMUX can coexist with its disc source; conflicting primary sources
are rejected. `HDR` accepts `sdr`, `hdr10`, `hdr10plus`, `dolby_vision`. Absent HDR
tags never imply SDR. SDR with an HDR claim conflicts; DV and HDR10 can coexist.

`Languages` and `Subtitles` mean **all required languages**, separately. Supported
codes are zh, en, ja, ko, yue, fr, de, es and th; subtitle zh-hans / zh-hant require
explicit simplified / traditional evidence. Chinese subtitle tags do not imply
Mandarin audio. Unspecified dual/multi-language tags do not prove a language.
Language words in a repeated translated title are not audio proof; provider
subtitle text needs an explicit audio label. Contradictory no-subtitle claims
fail closed. These checks read release tokens, never assume a default language.

`Rank` applies `PreferredResolutions` in declared order, then `PreferredGroups`
in declared order. It only supplies a preference score; rejected resources stay
rejected. A conflicting or unknown resolution gets no resolution preference.

Validation: `go test -race ./internal/decision` and `go vet ./internal/decision`.
