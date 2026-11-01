# S01 qB completed private TLS — external candidate, not executed

## Source, current gap, authority

Fixed MoviePilot 8a83efeacd3e26b82be495e17d50146e31c96a60
`app/modules/qbittorrent/qbittorrent.py` SHA256
114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0:
`__login_qbittorrent` line203 constructs the qB SDK client with
VERIFY_WEBUI_CERTIFICATE=False. `get_completed_torrents` line254 calls
`get_torrents` line224 using that same self.qbc. This is not a RequestUtils client.

Current HEAD808148abfad0b7fe230c426387a3b7b49eeedb18 has a qB-only App selector,
then NewQBittorrentCompletedClient -> NewIYUUClient -> New. New uses the global
strict default transport through logging; the Completed constructor matches the
historically accepted bytes ea9a7952068a106671f8710fb780413f8afe91967adc7b10b24eac62517b2883.
It is genuinely the same qB service's independently implemented read client.
Parent/root10:29 clarified the user's original qB certificate-disable permission
is not restricted to command interfaces; it covers this qB completed client.
This does NOT grant TR, generic IYUU, global transport, Git/browser, cross-origin
credentials, or arbitrary unimplemented downloader methods.

## Exact patch boundary

One existing product file, one new test file, external only. The constructor
still validates qB kind and trusted guard and delegates ordinary configuration
to NewIYUUClient. Only this new client's transport is replaced with a private
http.DefaultTransport clone and private TLS configuration with skip-verify=true.
All other clone defaults, especially environment proxy and dialer, remain intact.
No fallback or alternate route is added. Wrapper order remains
Guard -> optional same-origin Bearer -> mutation dispatch marker -> logging ->
HTTP transport. Existing http.Client timeout20s, Jar, CheckRedirect deny-all,
connection config, mutex, query method, form, selectors, parsing, limits, source
error behavior and Close chain stay unchanged. Only owned idle-pool ownership
changes from global/default to this private clone.

NewIYUUClient, Client.New, commands, TR, logging and App/SDK remain byte-identical.
No GET torrents/list/downloading generalization; no source Python or real qB.

## Proposed exact small native gates

Normal and race each select only
`TestQBittorrentCompletedPrivateTLSAndScope` in
`./internal/plugins/iyuuautoseed/native`.

The new fixture explicitly allocates tcp4 127.0.0.1 ephemeral listeners, uses
standard httptest's owned self-signed TLS, and serves only the fixed completed
POST. It never creates a proxy, CONNECT handler, forwarding client or upstream
URL. Product and strict control use the existing transport route/environment.
No proxy/NO_PROXY/CA mutation or direct-fallback retry. Any route refusal or
failure stays failure, not skip or authorization to change paths.

Observe one successful completed POST, fixed raw response/form and dummy Bearer;
unchanged default transport rejects the same owned certificate with actual
UnknownAuthorityError; unchanged general IYUU factory also remains strict.
A second completed response is a fixed302 to another owned peer and must not be
followed; revoked guard and pre-cancel issue no more HTTP; qB constructor rejects
TR. Timeout/Jar/redirect presence and global transport/TLS pointers stay intact.
The target response is an owned protocol fixture, not newly executed source
oracle. No real daemon, App worker, ordinary CLI or whole-S01 completion claimed.

Historical source28/pure/native/App/CLI contracts are inherited at their exact
recorded layers. The old source/runtime seal is bound as a historical reference,
not mislabelled as a new source run. The old README's TLS restriction is retained
as historical documentation, superseded only by this later explicit qB approval.
The original query/rows/vendor functions are byte-identical. Any final full-parent
S01 closeout/publication is coordinated after independent evidence review; this
candidate must not be pushed as an isolated child slice.

The reused overlay runner performs full Go declarations/backing mapping plus
before/after-compile/after-test hashes and generated-source retention. Its existing
-vet=off flag means vet NOT RUN here; its inherited virtual-directory wording is
not an assertion of a new vet failure for this existing native directory.
