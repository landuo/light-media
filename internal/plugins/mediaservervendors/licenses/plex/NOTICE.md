# Plex SDK and Requests attribution

The Go Plex media-server adapter under `internal/plugins/mediaservervendors`
contains a source-derived implementation of the selected constructor, library,
section, profile and refresh behavior from python-plexapi (PlexAPI) 4.18.1.
The fixed reference is the official PlexAPI 4.18.1 distribution. This does not
identify or claim recovery of the historical installed PlexAPI version.

PlexAPI copyright and redistribution terms are retained verbatim in
`PlexAPI-BSD-3-Clause.txt` beside this notice. No endorsement by python-plexapi
or its contributors is implied.

`plex_sdk_status_codes.go` is a data-only Go map of the first canonical name
for each code in Requests 2.34.2 `requests/status_codes.py` (`_codes`). The
selected source table was extracted without importing Requests. Requests'
Apache License, Version 2.0 text is retained verbatim in
`Requests-Apache-2.0.txt` beside this notice.

The migration replaces selected Python objects and transport calls with Go
types and private HTTP/session adapters, preserving the accepted bounded
source behavior. Host authorization/current checks and redacted diagnostics
are target-platform adaptations. The status-code data is represented as a
Go integer-to-string map rather than the Python tuple-valued table. This is
not redistribution of, or an implementation claim for, every PlexAPI method.

The separate public Go testing-certificate fixture used by the App tests
retains its original Go Authors copyright header. Its exact original BSD
license is provided at `internal/app/testdata/LICENSE`; it is not covered by
the PlexAPI or Requests licenses.
