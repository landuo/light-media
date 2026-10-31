# Candidate02: original JSON hydration prerequisite

Candidate01 was never compiled or executed; its four source copies and original contract remain historical unchanged. Its statement that fail can ignore malformed JSON note was incorrect: original get_by_domain hydrates the entire ORM row and JSON decoding fails before fail's body.

Candidate02 reads note for fail and verifies the finite JSON syntax prerequisite. Valid JSON of any shape remains raw, including a JSON string; no timing-map conversion or unrelated column write. Invalid JSON raises an error and leaves the counter and all row bytes unchanged. Existing unsupported non-finite Python JSON values are outside this ordinary finite JSON boundary; this is not a claim of every legacy JSON value.

The existing one failure root now asserts valid JSON-string preservation and invalid JSON rejection. Its success(None) case with a null-valued note is explicitly the finite int64-note boundary, not a source exception claim. The actual source 19-call storage differential and all other roots are unchanged.

No Go, original-source, SQL or runtime execution by the owner. Exact two old hooks remain unchanged from candidate01.
