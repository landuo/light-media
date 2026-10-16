# SMTP adapter handoff

No formal files, frozen Merge files or root modules changed. Runtime source is `mime.go`, `address.go`, `client.go`, `binding.go`. All original algorithm sources, licenses, executable reference scripts, source metadata and streaming fixtures are retained. Python is an offline development reference only; there is no Python subprocess in the runtime.

## Exact interface

```go
BindingFromTrustedConfig(config map[string]any) (Binding, error)
New(binding Binding, options Options) (*Adapter, error)
(*Adapter).Send(ctx context.Context, request merge.SMTPRequest) error
BuildMessage(request merge.SMTPRequest) ([]byte, error)
```

`Adapter.Send` directly fits frozen `merge.Host.SMTP`. Binding must be reconstructed from the host's frozen `custom.email` component config, not worker JSON. The App owns instance/generation/revision, registration, save-origin facts, bound notice IDs, cancellation, effect receipts and uncertain-send recovery. Do not expose a worker RPC accepting SMTPRequest, Binding or Options.

Binding fields are Host, numeric Port, explicit Encrypt, Username, Password, FromName and unique Recipients. Options permits only trusted DialContext, TLSConfig, LocalHostname and Timeout. Default socket timeout is source 60 seconds; a context can impose a stricter lifetime. TLSConfig is cloned, including RootCAs; system trust is used when roots are nil, and InsecureSkipVerify is rejected. No automatic proxy, redirect, plaintext downgrade or resend is introduced.

## Later integration steps

1. Review `README`'s strict certificate-policy difference and dynamic/environment boundaries. Keep them visible; successful loopback checks are not real mailbox validation.
2. Put this module in an appropriate internal child package, changing only import/module paths to the selected formal Merge package. Carry all original PSF/GPL notices, licenses and provenance. Tests remain streaming gzip, with the original byte corpus intact.
3. Bind SMTP from a trusted immutable component-config snapshot. Only let a current owner call Send after verifying the event ID and current generation/revision. Retiring an owner cancels all its active contexts; never restore an old queued send into a new generation.
4. Connect `Host.SMTP = adapter.Send`. The callback verifies all request connection/recipient fields before dialing, even though the core built them from configuration. MIME raw serialization happens after login, matching original message.as_string timing; construction failures happen before dial.
5. Keep the existing `SMTPQuitError` escape semantics for QUIT and pre-try construction errors. If generalized, change the frozen Merge core and adapter together in a separate authorized window. Do not convert QUIT failure after possible delivery into safe-to-retry failure.
6. Do not replay a saved `test_once` flag on startup. Only the existing trusted save-origin lifecycle path may send it; store its reset and receipt with the surrounding owner transaction. An SMTP timeout after DATA may already have delivered mail and needs explicit uncertainty handling.
7. Retain SDK worker stop as worker cleanup only. It must not retire the still-live host owner after each invocation. Owner Stop/config disable/uninstall remains a separate lifecycle action.

## Source mapping

| Source | Go |
| --- | --- |
| EmailChannel.__build_message → MIMEText/set_charset/set_payload/as_string | prepare, prepared.serialize, BuildMessage |
| email.charset.header_encode_lines / Header._ValueFormatter | encodedWord, formatter, encodeHeaderRaw/encodeHeader |
| email.utils.formataddr / strict parseaddr and _parseaddr state machine | formatAddr, parseAddress, addressParser, quoteAddr |
| smtplib.SMTP.connect/getreply/EHLO/HELO | Adapter.connect, client.reply/hello |
| SMTP.starttls / SMTP_SSL._get_socket | client.startTLS / verified tls.Client before greeting |
| SMTP.login/auth/auth_cram_md5/auth_plain/auth_login | client.login/auth and pure Go HMAC/Base64 |
| binascii_a2b_base64_impl(strict=False) | decodeChallenge |
| SMTP.sendmail/mail/rcpt/data/_rset/quit | client.sendmail/reset/command, fixEOLs/dotStuff, Adapter.Send finally |

The source C Base64 interpretation uses official [CPython v3.12.13 binascii.c](https://raw.githubusercontent.com/python/cpython/v3.12.13/Modules/binascii.c); the actual installed extension is separately hashed and supplies all 8,651 execution references. Do not claim the official source download proves an unpatched installed binary; the measured behavior is the evidence.

## Verification to preserve

364 MIME raw/error cases, 216 Header stages, 6,237 strict addresses, 8,651 Base64 cases, 44 real source SMTP/TLS loopback conversations; dedicated CA/default-trust rejection; no unbound targets or recipients; cancellation closes owned sockets without retry; twelve concurrent sessions; actual frozen Merge core plus SDK Runner/Serve triggering real owned SMTP dialogue. Raw headers/DATA are compared byte-for-byte, not only MIME-decoded values.

The whole App, NAS memory profile, real production SMTP servers and delivery to real accounts have not been tested. This work does not solve Mako/Apprise, does not install MergeMessageNotify and must not be described as the complete app being ready.
