# Requests proxy merge and redirect source oracle

This is synthetic, offline frozen-source execution, not a live-site or installed-runtime execution report. `oracle.json` contains 100 source cases. The bounded Go transport comparison passes 85 cases / 252 URL hops; 15 cases are source-only and explicitly excluded, not parity claims. The Go supported subset is Session.request normalization, empty session proxy mapping, trust_env=true, and string-valued explicit entries (including present empty strings). Source-only coverage includes null-valued entries, session mappings, direct merge-method None semantics, trust_env=false, and an authentic invalid-port ValueError.

The generator AST-executes the selected original implementations, including Session.merge_environment_settings, merge_setting, Session.request's normalization assignment, utils.resolve_proxies/select_proxy/get_environ_proxies/should_bypass_proxies and their selected dependencies. All source os references point to a fake object containing only each synthetic environment, never process os.environ. Request and Session are SimpleNamespace holders for relevant fields. No Requests package is imported, no network is used, and Python is an offline evidence generator only, never a product dependency. Python standard-library parsing, socket conversion, collections, typing and contextlib primitives support the frozen functions; this is not a claim that the entire original interpreter/package is reproduced.

`frozen-source.json` records exact extracted source, full source hashes, source attribution and license paths. Official Requests v2.34.2 utils.py exactly matches the previously captured installed utils.py SHA-256. sessions.py and _types.py come from the same official version tag, but their installed-file hashes are NOT verified. urllib getproxies_environment/proxy_bypass_environment are the captured original Linux Python 3.12.13 functions from the existing original source artifact, not substitutes from the current interpreter.

Coverage includes explicit None/empty/value, present-empty scheme keys, per-host priorities, lowercase/uppercase/CGI environments, NO_PROXY/no_proxy and explicit overrides (including empty overrides), CIDR, initial bypass followed by redirects, and redirect map retention despite a later bypass target. Selection/map results are emitted by source execution, not handwritten expectations.

Replay from the repository root:

    python testdata/plugins/auto-signin-proxy-routing/generate-oracle.py
    go test ./internal/networkproxy -run '^TestRequestsMergeFrozenSourceOracle$' -count=1

Initial extraction (requires the attributed official files in the directory):

    python testdata/plugins/auto-signin-proxy-routing/generate-oracle.py --freeze work/auto-signin-proxy-source

The Go test verifies the frozen-source digest and consumes JSON only. Frozen Requests excerpts are under the Apache License 2.0; see `internal/networkproxy/licenses/Requests-Apache-2.0.txt` and `internal/networkproxy/REQUESTS-NOTICE`. Frozen CPython excerpts are covered by `internal/networkproxy/licenses/CPython-PSF.txt`. Source provenance: https://github.com/psf/requests/tree/v2.34.2/src/requests .
