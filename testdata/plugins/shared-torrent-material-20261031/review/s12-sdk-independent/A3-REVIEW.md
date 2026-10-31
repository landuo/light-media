# S12 SDK A3 independent review

**Finite PASS: source-exception envelope delta and all three current SDK roots.**

Current product `c6fb708a…` and test `5bfefaf3…` match the A3 manifest/archive. The accepted envelope carries kind=source_error and one finite exception class, never source message/body/URL/credentials/traceback. Missing/null/unknown class, extra tuple/content/handle fields, and source_error mixed into another kind reject. Accepted exceptions have no allocation handle and perform only prepare; all seven controlled-class subcases assert one call. A malformed mixed response with a valid handle still follows the pre-existing best-effort cleanup path, but is not accepted as a typed source exception. Ordinary host/transport/cancellation errors remain distinct.

Whitelist source grounding: original source19 actually observed only binascii **Error** and **JSONDecodeError**. UnicodeDecodeError, UnicodeEncodeError, AttributeError, TypeError and ValueError are grounded in fixed descriptor UTF-8/URL/query/dictionary branches; they are **static source classifications, not five additional original runtime observations**. Seven synthetic envelope tests prove classification transport only.

Actual `evidence/s12-sdk03`: old wire/boundary and paged/cleanup roots plus the new exception root all PASS; three commands exit0/empty stderr, no fail/skip. Ended `2026-10-31T18:30:48.757190+00:00`. All1924 before/after-compile/after-test/current bindings agree. Actual program SHA `1f827f6c54b88311081e4117a0d8b15a9bd6c2d5627cf633e19bc5bb6ee0b152` independently verified. Original test helper/root text unchanged, but decoder changed, so the two old roots' credit here is an actual rerun, not inheritance shorthand.

No actual host exception adapter, standard worker/wire, scope lifecycle, HTTP, cache, source-notification or complete S12 credit. No race/CI/delivery claim. Reviewer ran no Go/source/network and wrote only A3 audit files; earlier records remain unchanged.

Machine seal `A3-FINAL.json` SHA `11ee8accb90faa5abc96497832968358cfc044b87932a997020535333c902baa`.
