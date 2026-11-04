# Source-derived private Ugreen CookieJar adapter

The cookie-state and parser implementations are derived from Python 3.12's
`Lib/http/cookiejar.py` (PSF license and included historical notices) and
Requests 2.34.2 `requests/cookies.py` (Apache License 2.0), using the fixed local
source bindings recorded by the original collector. Keep both attached license
texts and this attribution with an eventual adopted source distribution.

This notice is a separate static artifact. It neither changes frozen Go bytes
nor asserts the whole Python CookieJar behavior has been verified.
