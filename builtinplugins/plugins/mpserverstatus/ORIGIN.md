MPServerStatus 1.4 is implemented in Go from the installed 1,017-line source
`mpserverstatus/__init__.py`, SHA-256
`63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90`.
The complete class has 42 methods. The actual RequestUtils source is
`app/utils/http.py`, SHA-256
`f8d90a731717de14a822d8035a14486caba2e25d52018c87d6ec0d9fcd3b2b27`.
No Python or original application runs in the product.

`status.go`, `render.go` and `unicode_table.go` initially reused the host's
pure `internal/plugins/serverstatus` algorithms. The old handler delegated
`server.status.configure/snapshot` to a built-in host business implementation;
that handler is not used. This worker owns the original parser, metrics, UI,
sampling and DNS/TLS caches. A transport-local non-replayable empty body
prevents Go from silently retrying a dropped reused GET socket, while
retaining the source's bodyless wire and 307/308 redirect handling. Ordinary source logs use the existing typed SDK.
Its only host capabilities are `network.proxy.read` and `plugin.log.write`.
No generic business executor, journal, workflow, AI or credential API is added.

The original form contains only `enable`. Its returned default is the current
Python truth value of that member, including for partial/nonboolean inputs.
Init clears three memory caches after successful config access. Page reads the
enable gate once; dashboard deliberately has no such gate. Stop is a no-op.
Requests already in progress may refill caches after init or stop. There is no
whole-probe lock, scheduler, command, API, event handler, persistent KV/history,
file migration or one-shot flag. The inherited renderer is Vuetify. Dashboard
metadata is inherited `None`, so the platform owns its default empty-key card.
Official authentication level 1 is excluded by the migration scope. Clones are
platform instances sharing this binary, each with independent process memory.

HTTP retains the fixed public `https://movie-pilot.org/status` target. No worker
payload supplies a URL, timeout, TLS policy, DNS name or shell command. Each
request gets a fresh private proxy environment snapshot. RequestUtils does not
pass Settings.PROXY here: only its Requests trust-env routing is used. The
pure `requestsproxy` library reuses host environment/redirect-route semantics,
SOCKS4/4a/5/5h, Latin-1 proxy auth, cancellation and idle timeout logic. Native
connect first resolves, then tries each address serially with its own timeout,
following actual Python3.12 socket.py and urllib3 connection.py. Its
source-file SHA bindings are retained with the preparation evidence. No App or
host framework is copied. Required Requests/PSF/PySocks notices are alongside
that library. The pure `encodingdetector` code and embedded tables/model files
retain their PSF, Unicode and chardet licenses and per-file SHA inventory.

The exact original image is
`sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881`.
Its Python 3.12.13, Requests 2.34.2 and urllib3 2.7.0 support Brotli 1.2.0 but
lack backports.zstd. Therefore the source wire advertises `gzip, deflate, br`;
source zstd bodies remain encoded. An older local Python 3.14 network fixture
incorrectly implied zstd support for this NAS; it is retained separately as
historical evidence and is not the current network oracle. The Go module pins are `github.com/andybalholm/brotli v1.2.6` and
`golang.org/x/net v0.61.0`. Their notices are `third_party/brotli-LICENSE`,
`third_party/brotli-flate-LICENSE`, `third_party/x-net-LICENSE`, and
`third_party/x-net-PATENTS`. Original Requests LICENSE and copyright NOTICE
are in `requestsproxy/licenses/Requests-Apache-2.0.txt` and
`requestsproxy/licenses/Requests-NOTICE`; the helper's PSF/PySocks licenses
are beside them. `encodingdetector/LICENSE.chardet` is the actual chardet
7.4.3 0BSD notice; its PSF/Unicode notices are in the same directory. There is no klauspost/zstd dependency here.

`reference/source-original-oracle.json` (174 cases), `extra-source-oracle.json`
(29 cases), `source-http-reference.json` (19 cases) and
`source-compression-reference.json` (11 cases),
`source-encoding-reference.json` (110 cases), and the single redirect/drop trace were produced before comparison
from actual source/vendor code in new disposable containers using that exact
image. Core probes use synthetic network/time boundaries; network probes use
only a generated certificate and container loopback. No production app import,
configuration, DB, credentials, external requests or notifications were used.
The temporary containers were removed and marker directories cleaned. Tests
consume these source results; Go output is never an expected-value oracle.

Necessary runtime boundaries and remaining actual-host verification:

- Approved credential boundary: HTTPS proxies have a separate verified TLS
  connection before any Proxy-Authorization or CONNECT is written. The
  destination keeps its original verify=False behavior; proxy credentials never
  become destination Authorization. Redirect URLs carrying userinfo or
  Authorization are rejected. HTTP/SOCKS auth is confined to the current
  canonical private snapshot. No private keys or global verifier changes.
- Source HTTP's `verify=False` applies only to this public status probe. Direct
  TLS details retain verification, system roots, SNI and the five-second source
  timeout. No global transport or verifier is changed. Go/native DNS, socket,
  TLS and proxy failures have native diagnostic wording rather than CPython/
  OpenSSL exception wording. Proxy userinfo is redacted from diagnostics.
- Requests can implicitly consult `.netrc`; this uncredentialed probe does not
  read account files or send user credentials. Proxy credentials remain in the
  private SDK snapshot for the admitted call only. Cookies, if set by the public
  server, live only within that request's redirect session.
- Context cancellation and rejected host scopes propagate as errors and stop
  further probes, rather than being converted into an ordinary source network
  failure. A canceled caller does not replace the prior DNS/TLS cache. This is
  an approved typed SDK runtime adaptation; the Python source has no caller
  context. Native five-second TLS/read-idle timeout and certificate failures
  with an active caller still follow the original failure-cache TTLs. The
  existing finite SDK log/response limits remain in force.
- Pure-Go DNS uses the Go/OS resolver and preserves its result order before
  source de-duplication. OS resolver configuration, Go versus libc address
  ordering, root certificate stores and runtime diagnostic text need explicit
  actual Linux-host observations; loopback tests do not prove public DNS or the
  public certificate chain. No ordinary Main/browser/clone acceptance is claimed
  by the unit or executable SDK drivers.

Independent source review corrections are pinned to the same historical SDK95
closure rather than silently using newer SDK cleanup changes. The date adapter
uses the actual Python3.12 integer lexical rules, arbitrary integer overflow
handling and Unicode15 str.isdigit (83 ranges), including unknown-zone versus
out-of-range-zone behavior. The original c23b/4a5a candidate and failing source
comparison logs are retained as superseded evidence.

The private origin plaintext connection adapter preserves http.client's
physical header count100 (including folded/duplicate lines and terminal blank)
and65536-byte status/header line bounds. It is rearmed at each GotConn request
epoch, including the same socket after redirects. Only source100 informational
blocks are skipped. Other final1xx status/protocol/header metadata, Python's
status integer/version lexical behavior and colonless-header termination are
retained before passing ordinary body framing to net/http. CONNECT uses the
same finite source status/physical-line boundary; the HTTPS proxy remains
independently verified before auth, and origin TLS retains source verifyFalse.
This is a private fixed-probe protocol adapter, not a host HTTP/RPC framework.

Response text is a pure getter over already-loaded content bytes and charset.
It runs freshly at both original property reads: once in Probe after elapsed
HTTP timing/logging, then again before Parse after optional DNS/TLS. Only
LookupError/TypeError fall back to UTF8; other codec errors escape at the source
property boundary, without a request-error banner, parse warning or new sample.
No HostCaller, private proxy snapshot or environment is captured by that getter.

The request-local cookie jar follows the original default Netscape policy,
including its effective-host `.local` rule, single-label Domain rejection,
liberal host-only return matching, and retained domain/path insertion order.
It does not substitute a public-suffix list. Raw Max-Age uses Python's integer
lexical rules: an invalid value drops the cookie, zero/negative values expire
it, and the last repeated attribute wins. Source fixtures cover ordinary and
local domains and these expiration branches; cookies never persist between
logical status requests or supply private account credentials.

2026-11-06 source-checked declaration correction: the manifest omits `get_service`. The independent actual-Python3.12 source inventory distinguishes inherited/explicit `pass` from an implemented `return None`; compatible null Handle branches remain. API/page/auth declarations retain their separate source order. See [interface source and host dependency](../../tests/sourceinterfaces/ORIGIN.md). This new executable requires the finite host optional-lifecycle/discovery-gate/default-dashboard correction; older published host75e13686 rejects its artifact acquisition. SDK98 transport remains unchanged. The coordinator binds the exact new host commit separately; this note does not invent a released minimum version.
