# B5 non-string log-order correction

**Finite PASS; the recorded three-case missing-INFO finding is closed.**

New source observation preserves complete original wrapper/normalizer/helper method AST, but helper execution only reaches int.startswith at line83. Absent/relative base emits INFO before that AttributeError; valid base fails in original urllib before INFO/helper entry. Its full15318-byte raw is embedded byte-identically and independently accepted in B5-SOURCE-ACCEPTANCE.json. No old source19/raw/finding was rewritten.

Production fix parses base first using a thin delegate to existing sourceParseURL, preserves base parse errors, and emits INFO only for false/originless base before classifying nonstring failure. No invented URL probe or str(123). The target string-only Material port is not called: test compares exception class and log sequence, not source helper-call trace or Python exception text.

Actual s12-wrapper-b501: only TestDownloadTorrentActualSourceNonstringLogOrder and its3 subcases PASS;3441 inputs before/after/current match, three command exits0, no skip/fail. Ended19:01:56.250820 UTC; actual program19e433d9… verified. Four final source files/archive match. Other wrapper roots were not rerun by this increment; previous finite credits remain historical rather than a blanket current-package PASS.

No general surrogate/all-input parity, real logger/HTTP/cache/worker/host, wholeS12/race/CI/delivery claim. Reviewer ran no Go/source/network.

Machine seal B5-FINAL.json SHA 0c8e0090c2b18ea9c2523e73952d59fbaded2b3794e3828745706c7281a021c5.
