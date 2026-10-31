# Dedicated qB TLS authorization — candidate 02, unexecuted delta

The coordinator relayed the user's explicit 2026-10-31 **21:55:10** approval, `Sentinel_6f870ab66a148191a118376aa072856d`: disable HTTPS certificate verification **only for this independent qB client**. This is not a global TLS/CA/environment/Git/browser authorization and not permission to operate real downloaders. The coordinator owns the authoritative ledger entry.

The source constructor's `VERIFY_WEBUI_CERTIFICATE=False` is now implemented in `NewSharedDownloaderCommands` only after `http.DefaultTransport.(*http.Transport).Clone()`, inside the qB branch: initialize its owned TLS config if nil, then set `InsecureSkipVerify=true`. `Transport.Clone` clones any existing TLS config. No old IYUU/native constructor or TR branch, environment/proxy selection, redirect rule, guard, credential origin, factory signature, timeout, payload bound, or selected-mutation behavior changed. The App factory remains byte-identical.

This supersedes only the previous “qB source TLS=False pending/unapproved” statement for this precise new client. The old candidate01 and its normal5/race1 evidence remain historical and unchanged; they do not retroactively prove the new TLS code. All other declared source/domain/safety limitations remain. `TRANSPORT-ZERO-WRITE-CLARIFICATION.md` still governs retry wording.

New prepared root: `TestSharedDownloaderCommandQBTLSIsolation`.
- Dedicated qB fixed control performs a real HTTPS request to owned self-signed `httptest` TLS origin using synthetic configured Bearer.
- Existing IYUU client and new TR command client reject the same self-signed origin before HTTP handler entry.
- Owned cross-origin redirect is not followed; no request/credential reaches the second owned peer.
- Current guard still denies before dispatch; global DefaultTransport and RootCAs are unchanged. No global test TLS/CA injection or proxy/environment clearing is used.

The coordinator's narrow planned gate is new TLS + old QBControls/QBAdd/Guard normal (4 roots), then existing Guard race (1 root), followed by actual App worker gates separately. Historical TRControls/TRAdd observations remain their own old evidence, not newly executed results. This owner ran only formatting and static hash/archive checks, not Go compilation, tests, TLS/network requests or source imports.

Affected closure: the native package and importers such as the App fixed factory need new build bindings. SDK-only fixture protocol bytes are not changed by this patch. Product diff is confined to `internal/plugins/iyuuautoseed/native/shared_downloader_commands.go`; test diff imports x509 and appends one root to its existing test file. All previously executed five roots remain source-byte identical. The frozen host seven files, leaf and old native files remain unchanged.
