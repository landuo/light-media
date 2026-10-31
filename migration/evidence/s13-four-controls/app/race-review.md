# S13 App controls — actual component race acceptance

**ACCEPT the two exact component roots**, independent non-author review, 2026-10-31 UTC. `evidence/s13-four-app-race01`: session 61503, initial `b1ce03`, terminal `725373`, exit 0.

- Actual race list/build and test2json exits are all 0. `TestOriginalDownloaderControlHostWire` and `TestOriginalDownloaderControlHostScopeAndNoReplay` both ran and passed; zero skipped/failed events and no DATA RACE warning.
- **6,235 inputs** match before/after compile/after test and an independent current rehash. Program SHA256 `195f9e5bf508ad3addc31f976911a28201511001cf7c3c250dad327ed812681a`; generated testmain original/cache/preserved bytes all match.
- Earlier App normal01 has **6,224 inputs**, not the identical closure. The new race closure adds the two now-integrated native four-command files plus race-specific sources; the exact comparison is 22 added paths / 11 removed / zero changed common paths. The App handler, its two-root test and resource file with the two reviewed hooks are identical in both runs. Preserve the old normal result as its actual historical closure; do not relabel it as a run of this new closure.
- These roots use the explicit private factory/component seam, not the real installed worker or native HTTP. Their finite runtime acceptance complements, but does not replace, the separate native and normal CLI evidence. No new test, source import, process probe or product edit was performed by this reviewer.

Machine readback `app-race01-runtime-readback.json`: SHA256 `7ef1d11e04927877a41ae435ae1987bbdba674352d3249e590dacdc3f27268f0`.
