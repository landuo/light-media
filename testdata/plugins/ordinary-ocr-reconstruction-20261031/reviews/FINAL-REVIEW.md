# S15 reconstruction — final independent review

**ACCEPTED for publication as the ordinary SDK → active host → OCR helper feature slice. No remaining substantiated blocker was found in that slice. This does not mark the S15 parent, all providers/business plugins, or the full platform complete.**

## Actual implementation and acceptance

The final 22 files (19 new, 3 existing) match their frozen hashes. The implementation provides the ordinary SDK call and lossless typed result/error envelope, source-derived helper normalization and result handling, OCR-only private HTTP/TLS policy, and active-host capability/configuration snapshot checks. The real compiled worker and normal server exercise the public installation/API path. This is usable functionality, not only a probe or an unconnected helper.

Only OCR's dedicated transport skips certificate verification. Ordinary BoundSiteHTTP retains strict TLS, its original body limit and redirect protection. Global transport/CA/proxy state is not changed. Image credentials are scoped to the configured origin with unchanged configuration/enabled state; foreign-origin credentials are stripped and scope is checked at redirect/return boundaries. OCR POST does not inherit image Cookie/UA. The endpoint is a process-start snapshot after source-style configuration stripping, with literal suffix semantics. The extracted response codec retains the delivered IYUU logic; IYUU itself is unchanged.

New actual gates, independently read back:

| Gate | Actual result and scope |
| --- | --- |
| SDK01 / core01 | 1 / 4 roots PASS on their recorded bytes; pure helper observations are synthetic response/class-AST evidence |
| A-fix01 | 2 affected roots PASS after invalid UTF-8 rejection and dotted-I dataURL correction; unchanged earlier core results inherited, not rerun |
| Transport02 | 12 roots PASS: 5 OCR + 7 existing adjacent controls; 2,942 inputs |
| App01 | 7 roots PASS; real separately compiled SDK worker, host scope/snapshot, failure/recovery/cold; 6,152 inputs |
| qB neighbor01 | 1 existing dispatch-scope root PASS with four subcases; exact App program reused plus fresh owned worker |
| Server01 / normal CLI01 | server build PASS, 3,944 inputs; two real normal host processes (24 → 52), two SDK workers (40 → 66), five OCR calls, two natural host Wait=0; standard install and business responses checked |
| Race transport/App | 3 + 3 roots PASS, no fail/skip/DATA RACE; fresh race worker and both test programs; go-list/build/test compilation consistently use `-race`; 2,951 / 6,161 inputs |

All declared normal/CLI/race input maps were checked against their execution phases and current bytes again at final seal. Generated test-main copies also match. The sole historical path substitution is explicit: Transport02's executed driver is preserved byte-for-byte at `evidence/s15-transport02/driver.py`; the common runner was subsequently revised for future race gates. It is not falsely described as unchanged at the old path. Proxy/environment boundary assertions remain per actual gate; no full-environment identity or continuous resource-monitoring claim is made from phase samples.

## Source evidence, kept at its true layer

Fixed `8a83efe…` OCR/RequestUtils source identities are bound. The helper's 52 current class-AST cases demonstrate control flow and typed values, not HTTP execution. Accessible, committed `42d6` RequestUtils observations supply only the documented intersecting GET/header/status/codec/redirect/TLS cases; their original environment differs from the currently located venv. Their full historical generator/dependency closure is not reconstructed here.

Source preparation02 now actually exits 0. It executes 25 selected unmodified source AST nodes from 12 bound files with controlled stdlib JSON/session headers: the two bodies are 22 and 50 bytes; Accept/UA None removal, ASCII escaping and Content-Length are observed. The ASCII JSON body intersects the new Go owned-wire assertion. It does **not** execute requests/urllib3 package imports, URL preparation, Session.send, HTTP, a TLS/proxy handshake, or a new IPv6 probe. The Unicode case is preparation-only, not an OCR image-validity claim. Original preparation01 failure (missing standard builtin `__import__`) and empty capture remain intact.

## Explicit non-blocking limits and preserved failures

The parser's depth-1000 guard is **not proven to be the source decoder's exact recursion threshold**. No fixed-source counterexample establishing an ordinary-call behavioral regression was produced; reading `sys.getrecursionlimit()` is not such proof. Likewise, private synchronous TLS policy for both origin and HTTPS-proxy handshakes is statically justified, but this reconstruction does not newly exercise the two-hop proxy branch or CLI HTTPS. These are unverified edges, not known differences waived by the word “finite.” A later concrete ordinary-input counterexample remains a defect to fix, not an authorized divergence.

Accordingly these limits do not substantively block this connected ordinary-helper feature's acceptance, but prohibit claiming arbitrary Python object/input equivalence, exhaustive depth/proxy parity, a fresh whole-source HTTP differential run, real OCR recognition quality, all providers, all S15, CI, or deployment. Existing stricter cross-origin security boundaries are intentional and retained.

Transport01 remains a precompile driver/go-list failure, not product compile success; source preparation01 remains failed. Neither is overwritten by later PASS. Missing old-workspace S15 candidates/logs were not reused as new acceptance, and three planned-but-unexecuted HTTP oracle JSON files were not fabricated. Publication must still perform its separate whitelist/commit/push/remote-readback gate before claiming delivery.

Reviewer performed only static reading, hashing and audit writes—no Go, source imports, services, network, or product/ledger changes. Exact bindings are in `final-seal.json`.
