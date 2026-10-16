# MergeMessageNotify SMTP dependency — isolated Go candidate

This candidate supplies the named SMTP dependency of the frozen `work/_merge_message_notify_port` core. It is not registered in the App, not deployed and has never sent a real email. All accounts, recipients and messages are synthetic; all SMTP traffic went to owned loopback servers. Formal code, main modules, frozen Merge files and old service configuration were not modified.

## API and host authority

```go
b, err := BindingFromTrustedConfig(owner.ComponentConfig("custom.email"))
a, err := New(b, Options{ /* host-only TLS/Dial/LocalHostname options */ })
core := merge.New(merge.Host{SMTP: a.Send /* other dependencies */})
rawLF, err := BuildMessage(request) // offline MIME byte API
```

`BindingFromTrustedConfig` takes an unprefixed component config snapshot owned by the trusted host. Its name is a caller precondition, not an authentication mechanism. Worker input must never be passed here. The binding freezes host, numeric port, explicit `NONE`/`SSL`/`TLS`, username, password, sender name and unique recipients. Unknown configuration fields are neither read nor rewritten. Before dialing, `Send` requires the request's connection fields, credentials, From identity, name and exact recipient multiset to match the binding. Recipient order can follow the source's set-derived order without expanding authorization. Config and recipient slices and trusted CA pools are copied.

The child worker can only invoke frozen Merge capabilities with an opaque host-bound event ID. It cannot supply SMTP targets, recipients, CA pools or TLS flags. The real SDK subprocess test initializes the frozen Merge owner, dispatches an owned notice, executes its email channel through this adapter, and compares every actual SMTP command and DATA byte to the installed Python reference. The SDK worker's routine stop does not retire the long-lived owner.

The adapter does not provide an arbitrary RPC endpoint, a generic email-sending public API, implicit TLS downgrade or automatic retry. Each send gets an independent connection. The owning App must still bind instance/generation/revision, serialize configuration changes, cancel retired grants and maintain durable effect receipts; those App lifecycle features are outside this isolated package.

## Implemented source pipeline

* Actual `MIMEText(..., plain|html, utf-8)` header order, 57-byte Base64 body chunks, RFC 2047 shortest Q/B UTF-8 words, From quoting/escaping, Unicode splitlines/lstrip, ASCII syntactic folds and original outgoing header validation. `Message.as_string()` defaults maxheaderlen to zero; source Header's internal bound is 1,000,000, rather than ordinary 78-column folding. Encoded-length accumulation is linear while preserving source greedy character boundaries. The original does not add a MIME attachment: `image` is inserted into an HTML img URL.
* Complete source strict `email.utils.parseaddr` pre/post validation and the `AddrlistClass` state machine used by `smtplib.quoteaddr`: comments, nested comments, quoted strings, escapes, local parts, domains, route addresses, groups, malformed fallbacks and original domain-literal rejection behavior. The previous Go net/mail approximation was removed.
* SMTP/SSL connection greeting, multiline replies with source 8,192-byte line bound, EHLO/HELO fallback, old `auth=` feature syntax and repeated AUTH lines. Feature payloads are decoded as Latin-1 before source whitespace handling.
* Source authentication preference CRAM-MD5 → PLAIN → LOGIN, initial responses, 503 already-authenticated acceptance, authentication-method fallback, exact challenge-count limit and ASCII encoding failures. Challenge Base64 follows non-strict `binascii_a2b_base64_impl`, including ignored bytes and discontinuous/excess padding; Go's stricter Base64 decoder is not substituted.
* STARTTLS feature checks and response handling, TLS handshake followed by discarded old features and a fresh EHLO before login. SSL wraps before the greeting. TLS never falls back to cleartext.
* Original `sendmail` ordering and result behavior: SIZE counts ASCII CRLF message bytes before dot stuffing; source envelope quoting; MAIL/RCPT/DATA; success if at least one recipient succeeds; RSET on the exact source rejection paths; 421 closure; DATA initiation rejection differing from final DATA rejection; QUIT returns non-221 replies without inventing an exception.
* Original finally boundary: connection/STARTTLS/login failure occurs before assignment to the outer smtp variable and sends no QUIT. After successful login, serialization and send failures still attempt QUIT. A QUIT transport/protocol exception overrides the previous result via frozen `merge.SMTPQuitError`. Connections owned by Go are closed deterministically even where Python relies on object destruction/GC.

`Error` contains only a fixed class, phase and optional numeric status. It never includes server reply text, message content, URLs, mailbox identities or passwords. Context cancellation closes the owned socket; read/write deadlines refresh per underlying I/O, and an uncertain send is never retried automatically.

## Actual installed reference evidence

The installed EmailChannel raw SHA is `dd10781fe8cf50d243a5c35c150b34c627bbeedd5348002b7ce4379b9608a079`. The copied actual Python 3.12.13 sources retain original PSF notices. Installed smtplib SHA is `f19369e751e199f5dfc05882e2f77b6a692719cebd8a18c84552544a0b93e963`. Runtime metadata records Python 3.12.13 / OpenSSL 3.0.20 and the actual binascii extension SHA. Reference generation checks pinned plugin bytes and uses real installed email/smtplib/crypto library code, controlled app/config/log/clock inputs, and owned SMTP/TLS listeners. It never imports the running application or reads its config/credentials.

| Corpus | Cases | Comparison |
| --- | ---: | --- |
| Original EmailChannel MIME | 364 | 356 exact LF raw messages/SHA; 8 original header errors |
| Actual Header.encode stages | 216 | Raw header values and exceptions across no-fold and small greedy limits |
| Actual strict address/quoteaddr | 6,237 | Full parsed name/address and quoted envelope text, including nested comment/group cases |
| Actual binascii/Base64 | 8,651 | Exact decoded bytes or original Error class; all byte values, short combinations and padding variants |
| Real SMTP/TLS loopback | 44 | Every SMTP application command and DATA byte, original True/False/escaping exception result |

All gzip NDJSON corpora are streamed case by case. TLS application-wire reference covers NONE, SSL, STARTTLS, new EHLO, authentication preference/fallback, challenge loops, source response bounds, MIME serialization failure after login, MAIL/RCPT/DATA rejection/reset paths and QUIT overriding behavior. The encrypted TLS record stream is naturally not byte-identical across libraries/random nonces and is not claimed as such. No mock SMTP function replaces network dialogue in the 44-case corpus. EOF after a failed Python login is completed by explicit synthetic harness GC; it is not falsely attributed to an original QUIT call.

The 1,000,000-character original Header path was not executed with a million-character reference because the original loop is quadratic. Its same real `Header.encode(maxlinelen=...)` stages are exercised with small limits; the Go implementation retains the actual production bound. Do not call this a million-character installed raw-byte measurement.

## Deliberate strict TLS difference

The actual source default context has `verify_mode=0`, `check_hostname=False`, and minimum TLS version 771 (TLS 1.2). That behavior is saved in `reference/wire-metadata.json` as evidence only. The parent explicitly required the Go production adapter to verify certificates using system roots or trusted host-provided CA/ServerName, and to reject `InsecureSkipVerify`. The owned TLS tests use a dedicated CA and verified server name, never disabled verification. A separate test proves an untrusted self-signed peer is rejected. NONE is accepted only when explicitly selected in the trusted binding. This SMTP decision does not extend the user's separate local fnOS certificate exception.

## Explicit remaining input/environment boundaries

The MIME/SMTP algorithms above are implemented for the typed frozen request/binding contract, with the actual cases measured above. Do not imply all arbitrary Python object inputs or all OS/OpenSSL environments are equivalent:

* The frozen Merge `SMTPRequest` uses string fields. Its core already erases some original non-string title/text/from-name/credential types. Truthy non-string names and credentials are rejected by the trusted config binder rather than silently stringified; original behavior can depend on AUTH method (for example numeric password via f-string PLAIN versus `.encode()` in CRAM). Preserving every such dynamic error/order requires a later core contract change, not a fake implementation inside this adapter. None versus empty title has the same source raw header in the normal path.
* A MIME-construction exception that happened before the source try block is carried through the existing `SMTPQuitError` escape marker before any connection. The marker's name is narrower than this use; its payload retains the actual error class/phase. A later core API can rename/generalize the marker without changing network behavior. Serialization exceptions occur after login and remain ordinary failures followed by QUIT.
* SMTP hosts are ASCII and ports are positive numeric bindings. Python/socket IDNA conversion, service-name ports and arbitrary socket argument types are explicitly unsupported instead of being sent to a guessed address. Actual form use is a numeric port. IPv4/IPv6 numeric hosts are accepted.
* Address-parser recursion above 400 active parser levels returns `ErrUnsupported`. This is an explicit resource guard, not a claim that CPython's global frame-dependent RecursionError threshold is reproduced. Such inputs are not accepted as verified equivalence or sent with a guessed address.
* The default EHLO local name depends on the current host/container and Go DNS/hosts resolution. Trusted `Options.LocalHostname` pins it for source-wire comparison. Exact libc/NSS alias selection across machines is not claimed. Caller-provided Date is serialized exactly; the adapter does not claim to implement the original pytz database.
* TLS uses Go crypto/tls and the stricter certificate policy above; it does not emulate every OpenSSL cipher suite or historical TLS environment. Production server/account compatibility, App grants/recovery, NAS memory limits and real email delivery were not tested.

Mako and Apprise remain untouched in the frozen Merge candidate and are not solved by this SMTP package. This candidate does not by itself complete the whole MergeMessageNotify plugin or application.
