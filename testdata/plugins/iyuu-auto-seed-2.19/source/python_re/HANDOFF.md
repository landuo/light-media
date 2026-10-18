# GPW Python str regex handoff

The Go source/API/data are frozen for the `re.search(str)` closure used by the
installed IYUU GPW resolver. This module is independent at
`next/phase4/python_re`, module `lightmedia/pythonre`, Go1.23, no dependencies.
No phase3/URLprep/formal/main consumer source was edited.

```go
pattern, err := pythonre.Compile("id=" + tid + "(?:&|$)")
if err != nil { return "", nil } // source GPW except Exception -> None
ok, err := pattern.Search(href)
if err != nil { return "", nil }
if ok { /* original first matched href + source urljoin */ }
```

Retain original XPath candidate/filter/order and empty-url handling. Pattern
construction must use the original pyString(torrent_id), without escaping or a
numeric-only restriction. Source code installs the regex inside its candidate
loop and catches compile/match errors as None. The parent owns actual consumer
integration; this module does not bypass site/owner/IO guards.

Verification completed locally:

- 178,625 original CPython3.12.13 program outcomes, including its re_tests corpus,
  arbitrary GPW interpolation, dense capture/repeat/branch/condition matrices,
  syntactic errors, named escapes, Casefix and possessive minimum semantics.
- 34,537,472 complete scalar-range source outcomes for Unicode/ASCII categories,
  casefolded ranges/literals, anchors/boundaries, Unicode identifiers and exact
  simple lower/upper/cased predicates.
- 16 goroutines x1000 concurrent searches of one compiled Pattern.
- Final normal handle54107 exit0,5.167s.
- Final full race handle20648 exit0,43.133s; no short skips.
- Final vet handle4231ed exit0.
- Linux/amd64 CGO0 production package build handlec2a971 exit0.
- Linux/amd64 CGO0 static test build handleecf98c exit0.
- NAS static test handle85457 exit0; complete identical source corpus and Unicode suite PASS, followed by verified container/stage removal.

`reference/source-manifest.json` pins the original CPython v3.12.13 public
sources, with complete source and original headers. Keep LICENSE.PSF,
LICENSE.Unicode and NOTICE.CPython when distributing derived Go source/assets.
The standalone tests/oracles do not become a product Python dependency.

All successful oracles use the fixed isolated public Python3.12.13 image,
networknone, UID65534, dropALL, no-new-privileges, read-only root/config/public
binds, empty env,256MiB memory and64MiB tmpfs. No original user container, config,
environment, credentials or seed data was read. One initial generated grammar
oracle was deliberately interrupted before completion because its long
synthetic hrefs caused prolonged nested backtracking; its driver exit130 and
verified owned cleanup remain in the report, and no result was claimed from it.
The same operators were rerun with bounded synthetic subjects and passed.

Remaining differences are explicitly scoped in COVERAGE.md: diagnostic wording,
nonrepresentable Go byte strings, and original interpreter mutable resource
policy/current call depth at extreme patterns. There are no known unimplemented
functional string-regex branches within the specified consumed closure.
