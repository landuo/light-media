# Installed legacy metadata reference

This directory contains offline reference generators, not runtime dependencies.
All titles/configurations are synthetic. The scripts import the installed third
party packages and isolated installed AST classes; they never import application
modules, read user configuration/database/media, or acquire credentials.

The source is the actual installed Anitopy **2.1.1**, CPython **3.12.13**, Unicode
**15.0.0**, and cn2an **0.5.24**. Exact file/data hashes and fixture hashes are in
`../release-meta-legacy-python-provenance.json`. The independent Rust path uses
Unicode 17 and must not supply legacy predicates or mappings.

* `engine.py` instruments original methods only with a `finally` snapshot. It
  records every tokenizer/parser phase, token category/content/enclosed flag,
  ordered per-category values, alternative-number state, final dictionary, and
  original exception class. `generate.py` prints this harness plus the 10,271
  synthetic cases recovered from the committed reference. Execute that script
  with the original installed Python interpreter. Generated large raw JSON can
  be gzip-compressed deterministically; the Go test streams one case at a time.
* `wrapper.py` and `wrapper_unicode.py` invoke the actual installed MetaInfo /
  MetaAnime constructor in legacy mode after isolating AST dependencies with
  synthetic settings. They preserve original class code and source-SHA checks.
  Each input executes in a separate process with a 2-second boundary. The
  retained reference contains 461 anime/exception cases: 422 original cases and
  39 additional Unicode/name cases retained from 64 submitted inputs. Seven
  cases do not terminate in the source. The Go port detects that actual repeated
  parser state and returns `source_non_terminating`; it does not pretend the
  original produced metadata.
* `extract_installed_data.py` enumerates every valid Unicode scalar and extracts
  Python `isdigit`, `isalnum`, Unicode names/Latin names, NFKD plus combining-mark
  removal plus uppercase, decimal values, and full lower/upper/title mappings.
  Cased/CaseIgnorable come from the actual read-only CPython Unicode APIs. It
  also captures all 176 keyword and 29 extension entries with flags. Re-running
  this script in the installed interpreter reproduced all three captured data
  objects exactly. `../release-meta-legacy-python-data.json.gz` retains them and
  a Go test compares every generated table entry/range and keyword flag.
* `route.py` records the actual four compiled routing patterns/flags and source
  decisions for all engine titles plus bracket/dash/season/episode structure,
  separator, length, and Unicode combinations. Go compares every decision.
* `extract_installed_routing.py` captures MetaInfo routing from actual regex
  2026.5.9. Its digit/space and used-letter IGNORECASE sets differ from Python15;
  Garay/Tolong routing cases are in the actual wrapper reference.
* `cn_numbers.py` invokes actual installed cn2an smart conversion followed by
  `int`, as used by StringUtils/MetaBase. Its 1,073 captures cover malformed
  Chinese numerals, Arabic runs, leading zeros, unit conversion, and float/int
  size boundaries. `cn2an-name-validation.json` retains the exact source regular
  expressions and low-mode digit/unit maps consumed by the necessary slice.
  The source cn2an MIT license is `internal/releasemeta/CN2AN-LICENSE.txt`.

The Go engine retains MPL-2.0 in its own directory and the Unicode data license.
The parser preserves source errors such as AttributeError/ValueError/re.error;
MetaAnime catches ordinary source parser/matcher failures in assignment order
and retains partially initialized metadata. The source nontermination guard is
an explicit behavioral difference needed to return control to the host.

These fixtures validate this single-title path and its consumed metadata. They
are not proof that the whole MoviePilot identity resolver, all arbitrary regex
configuration, MetaInfoPath ancestry merging, or every plugin has been ported.
