# Installed SMTP dependency and implementation boundary

Target: installed MergeMessageNotify 1.20.1 `channel/custom/email.py`, copied read-only with SHA `dd10781fe8cf50d243a5c35c150b34c627bbeedd5348002b7ce4379b9608a079` (the final inventory verifies the actual copied bytes; do not infer this hash from upstream). The parent directory's installed-reference manifest is the authority if a draft hash differs.

The actual plugin uses `MIMEText(..., plain|html, utf-8)`, `formataddr((from_name, username))`, `Header(title, utf-8)`, and `message.as_string()` followed by `smtplib.SMTP/SMTP_SSL`, optional `starttls`, `login`, `sendmail` and `quit`. The `image` value becomes an HTML img URL. There is no file attachment, multipart message, SMTP `send_message`, SMTPUTF8 option, implicit POP login or third-party mail API to invent.

Dependencies copied from actual NAS Python 3.12.13 are under `reference/stdlib`, with their original PSF notices. Relevant paths are:

* `email/mime/{text,nonmultipart,base}.py`, `email/message.py` constructor/set_payload/set_charset/as_string.
* `email/{charset,base64mime,quoprimime,encoders}.py`: UTF-8 body Base64 and shortest Q/B headers, stable order and wrapping.
* `email/{header,_policybase,generator,utils,_parseaddr}.py`: Header/value formatting, formataddr and outgoing verification. `Message.as_string` defaults `maxheaderlen=0`; Generator changes policy max_line_length to zero and Header uses an internal 1,000,000 limit. Do not assume the usual 78-column wrapping.
* `smtplib.py`: response parser, EHLO/HELO/old AUTH feature syntax, CRAM-MD5→PLAIN→LOGIN with initial response and fallback, STARTTLS resets, `sendmail` SIZE/MAIL/RCPT/DATA/RSET, CRLF and dot stuffing, finally `quit` behavior. QUIT status !=221 alone is not an exception in `quit()` (unlike `__exit__`, which this plugin does not call).

Candidate API: `BuildMessage(merge.SMTPRequest) ([]byte,error)` returns exact original LF message bytes. `New(Binding, Options)` creates an immutable trusted namespace; `Adapter.Send(ctx, merge.SMTPRequest) error` directly fits the frozen Merge Host.SMTP dependency. The binding, not worker payload, determines host/port/mode/username/password/from name/recipients. Network requests are never redirected or retried after uncertainty.

Security decision from the parent task: original ssl._create_stdlib_context disables certificate validation; preserve that as source evidence only. Go defaults to system CA verification, or a trusted Options CA/ServerName. Reject InsecureSkipVerify. Workers cannot provide CA, TLS context, addresses or recipients. Tests trust an owned synthetic CA and never turn verification off. NONE is allowed only when explicitly present in the trusted binding; TLS does not fall back to plaintext.

All Python execution is offline reference generation with synthetic inputs and owned loopback SMTP/TLS servers. Product runtime remains Go. No real mailbox, credentials or production server is contacted. Formal code and the frozen Merge candidate stay untouched. Remaining dynamic-type/address-parser or wire differences must be listed precisely; neither a semantic MIME comparison nor successful raw-byte subsets alone constitute full SMTP completion.
