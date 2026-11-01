# S03 Cookie leaf01 — actual finite acceptance

**ACCEPT: pure recording-port algorithm, normal three roots. Native has not run.**

The coordinator's recorded actual execution is session54275, initial `aac97d`, terminal `ebf639`, exit0. `result.json` records 2026-11-01 07:18:52.990345–07:18:54.422460 UTC. I independently read the raw test2json events, compile/list/test exits and empty stderr, receipts, declarations and program rather than relying only on the PASS summary. No test/source rerun was performed.

- Program SHA: `16f1bc08e21e6ac52728068594c9f573132f7ebab854a8f455ece5e32a4cd90b`.
- **1,845 bound inputs**: before/after-compile/after-test maps identical; all current files also matched at this review time. The one generated test-main source copy matches its map hash. Nine supplied mappings remain binding inputs, but **no native package source was compiled or executed by this leaf test package**.
- Exactly `TestQBCookieOriginalSource17`, `TestDownloaderLeafSourceObservations`, and `TestDownloaderLeafBoundaryErrors` passed. No fail/skip event.
- All 17 original Cookie scenarios and their 18 Add subtests actually passed, including the same-client second merge. Runtime reports 52 outer recording calls and two false-success-member tuples which remain Python-truthy. The independently inspected test compares complete ordered primitive/log/commit traces, source tuple/type/truthiness and before/after/final stores against raw SHA `758df1e970dad525d564dc6732e2153f545dc8a0314fbbfd0b3e846156f02b6a`; it does not trim the invalid-key diagnostic or replace original errors with target expectations.
- The original 34-case leaf observation root and the existing boundary root actually reran; their earlier results were not merely cited. JSON object member order is the stated comparison normalization, not array/order/null/log removal.

This accepts the candidate06 pure algorithm on this exact finite original domain and retained boundary root. It does not establish native HTTP, vendor retries/response conversion, unknown selected-write behavior, daemon operation, App/worker/SDK/CLI, race or global URL/PEP440/Python-object equivalence. The source setter's recorded pre-effect RuntimeError is not evidence that an uncertain real write can safely fall back or replay. Native Unicode-derived assertions were not selected here, and the inherited Add layout-comparator difference remains for the separately planned native fix. Vet was disabled and is **NOT RUN**.

The hash readback is a timestamped fact before any later authorized candidate/gate updates; such updates do not rewrite this historical closure. Full binding details are in `actual-readback.json`.
