# S01: both original completed-task query operations

This package supplies the final known qB TLS dependency for the original S01 scope: **qB and Transmission `get_completed_torrents(ids,tags)`, including enumeration without pre-known hashes**. It reuses the already accepted behavior of those two operations. It does not turn broader all-task/downloading APIs, arbitrary downloader RPC, full service discovery, a real qB daemon or a new browser matrix into new requirements or claimed results.

## Original behavior and permission

Fixed MoviePilot 8a83efeacd3e26b82be495e17d50146e31c96a60 qB leaf SHA114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0 creates its qB client with `VERIFY_WEBUI_CERTIFICATE=False`; completed queries use that same client. On 2026-11-01 the root coordinator confirmed the user's earlier qB-only certificate-verification permission covers the same service's independent completed-query client, not only command methods. This is **not** an expansion to Transmission, global IYUU, Git, browser or other consumers.

The single product delta constructs a private default-transport clone only in the qB completed constructor. Existing proxy selection, timeout20s, cookie jar, no-redirect policy, mutation proof and per-request guard order remain. Global/default and old general IYUU remain strict. The old `testdata/shared-qbittorrent-completed/README.md` “Keep verification” sentence describes the 2026-10-31 pre-clarification candidate: **only that private qB completed TLS restriction is superseded here**. Its original text and historical run records are preserved, not relabelled as this run.

## Actual newly executed evidence

- Native01 originally failed the whole root at the global TLS pointer baseline assertion. Standard Go Transport.Clone's lazy HTTP2 initialization had not been stabilized before that baseline. The exact failed test/logs remain; earlier assertions within that failed root are not standalone passes.
- Test-only02 initializes that standard strict baseline before snapshot and keeps global RootCAs/TLS pointer checks. Product bytes are unchanged. Normal1 and race1 then passed, including actual self-signed owned HTTPS completed query, strict `UnknownAuthorityError` negative control, general-client strictness, guard/cancel/redirect and lifetime boundaries. These are protocol-fixture tests, not a real downloader daemon.
- On actual HEAD bd4ec4513e6ff9edc6eb4e0a357febf7d4ec30e0, the coordinator compiled normal worker766f111f… and normal serverbb787344… from one exact clean view. 4060 complete source/tool/metadata inputs matched before/after-worker/after-server. No tests or vet run is claimed by these builds. Three Meta hooks were HEAD, seventeen Meta Go paths hidden only in compile view, all25 original Meta files unchanged.
- The real ordinary CLI then started hosts23→53 and installed the unchanged standard worker38→68 against one owned self-signed HTTPS peer. Success200 → fixed info503/API502 → same worker recovery200 → cold query200; four logins/four infos, one index/one artifact, no query replay or cold refetch. Warm/cold401 and four observe-only calls had no qB side effects. Both hosts naturally exited0 and observed workers disappeared. 4094 frozen execution inputs and inherited environment remained unchanged. Controller stayed exact39123; no proxy/CA rewrite, fallback, upstream or CONNECT fixture.

The ordinary CLI's safe name/status summary is not a replacement for the old whole-field/source-oracle tests. Old qB source28/pure/native plus installed App/worker gates and original Transmission whole-leaf/vendor/owned-daemon/UI evidence are precisely referenced, not rerun. Historical initial-stop/recovery and request/response-limit/deadline/redirect adaptations remain disclosed. Dedicated old qB12/TR4 bytes and resource branches were independently checked; old full compiler closures are not falsely called unchanged after later capabilities were added.

## Acceptance / reproduction boundary

Non-author native, build, ordinary CLI and parent-reuse reviews are included. Together they close the known qB TLS dependency for whole-S01 composition; actual source adoption, sole ledger status, stage/commit/push/CI are separately recorded by the coordinator. This package alone does not assert a remote delivery or whole61 completion.

`full-binding-recovery-index.json` maps every original immutable input/phase/declaration map to a lossless content-addressed gzip. Decompress and verify the listed original SHA256/length; duplicate before/after phases deliberately share identical bytes. Saved commands retain original cloud paths and are evidence, not portable commands to run unchanged. Existing historical source/target evidence lives in the repository paths in `historical-references.json`.

Only one production source and one new native test are adopted. No SDK/App/worker/Transmission/Meta/B/38 source changes are included. No database, runtime secret, login response, TLS certificate/key file, program binary, raw environment dump or runtime private-data is in this publication proposal. Public Go testcert source is referenced by hash, not copied as a runtime key.
