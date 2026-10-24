# Standard plugin module integration, 2026-10-24

This milestone combines main bfb030cd2929c8418499ab358eee0459b7c4eaf3 and plugin branch 744a84398c2e606362cc2304a13dacff53755a1d. It replaces the retired plugin framework with the direct standard manager and preserves independent main catalog, site, UI and v0.4.18 changes. Existing plugin algorithm packages remain byte-identical to the task parent; this is not acceptance or adaptation of those legacy plugins. Unavailable adapters must remain unavailable rather than falling back to the retired runtime.

## Actual final-source verification

The frozen physical input contains 5,446 files (manifest SHA256 c868747e28ddb694546db42f85dc954b55f140c992f29366179f9cbd0082ad9d). Seven real worker fixtures were built from that input. Complete ordinary and race runs cover internal/app, internal/plugins, internal/plugins/artifacts, internal/plugins/schedule, internal/sites, internal/catalog and web: ordinary and race each total 849 passing top-level tests, zero failures and three explicit opt-in skips. App contributes 622 passes; SDK/artifacts/schedule together contribute 101. All seven packages terminated successfully. Race reports are empty. These are complete runs of the named packages, not a claim that all repository test packages ran. Build ./... and vet ./... both exited zero. Final input reread found no changed, extra or symlink files.

The skipped tests are TestIsolatedDownloadersRealTransferAndImport, TestIsolatedFNOSProductWorkflow and TestExploreLivePublicProviders. Their real-service coverage is not claimed. Earlier failing or timed-out candidates are not the evidence for this milestone.

## Retirement and preservation

The explicit absent-path set is 515 module retirements, 197 pre-existing main deletions and 12 earlier task retirements. Only paths actually present in a chosen parent require Git deletion. Old generated dist paths are replaced by the coherent final v0.4.18 build separately from source retirement. Retired old-contract tests are not counted as passing tests. Git history, historical evidence and user databases are not purged. Independent main changes and exact unchanged task evidence are retained.

## Assets and remaining limits

The physical production dist contains 100 files. Four historical gzip evidence blobs and the Brush PNG at both web/public/plugin_icon/brush-flow.png and web/dist/plugin_icon/brush-flow.png are retained by their official Git blob identities, not replaced with empty files. Both PNG paths use blob 51ed33cf7f5db91d61ad47cda576819d14dd4901 (1,442,036 bytes). These six carriers were not locally materialized. No claim is made that the tested local binary includes the restored PNG, or that a binary built after adding those assets is byte-identical to the tested local binary.

The actual final-main v0.4.18 frontend ran the original icon and utility generators, vue-tsc --noEmit and Vite production build, all exit zero. Its 100-file artifact manifest SHA256 is fcfd1457c743a4e593d4e8eb3408370d5e511e07d1bcbd6ea5895e87439266a9. Isolated browser checks separately exercised basic module flows, source uninstall and clone retention, reinstall and duplicate-clone feedback, upgrade from 1.0 to 2.0 with a clone retained at 1.0, repository settings, and a real unavailable AutoBackup card. Those browser checks used the earlier validated module backend and a test-only transport with production components, not the final main production login. PNG visual behavior was not verified. Frontend/browser evidence is a separate, isolated validation layer. The Go web package pass is not a claim of exhaustive final-browser or live deployment acceptance. This commit does not claim that legacy plugin business implementations have been adapted or accepted.
