# Local acquisition transfer error — limited factual record

This is an analyst record from the currently retained context, **not an original tool receipt**.

The first local source transfer formed a Bash command using `node -e ${JSON.stringify(code)}`. The JavaScript `code` contained a `writeFileSync` / `appendFileSync` call with `JSON.stringify(sourceChunk)` as its data argument. The shell's outer double quotes did not protect raw backticks / dollar expansion in that serialized source text. This was **not base64 quoting**; it created a possible shell command-substitution path from source data. It is not safe to assert that the transfer was harmless or that no shell-side execution occurred.

Known failed draft observations retained in context:

* `vendor-qb/torrents.py`: first local draft 81,257 bytes versus final pinned source 124,888 bytes; hash mismatch.
* `vendor-qb/request.py`: first local draft 45,301 bytes versus final pinned source 45,746 bytes; hash mismatch.

The precise initial tool chunk IDs, per-chunk wall-clock timestamps, stderr and exits were not retained in the present context or a saved receipt. They cannot be reconstructed as observed facts. The exact source chunk split and all shell interpolation outcomes are likewise not established by this record. No forbidden session-log reads or broad filesystem reconstruction were performed to fill this gap.

Those failed draft bytes were not used as source evidence. Subsequent writes used shell single-quote escaping, checked each write's exit status and verified every final source file against its pinned Git blob SHA-1 and byte length. `input-bindings.json` records the final content SHA-256. These checks establish **content integrity only**, not absence of prior side effects.

On coordinator request at approximately 21:33–21:34 UTC, further acquisition writes were stopped. Existing final sources are retained. No new source acquisition, vendor import, daemon invocation, repair deletion, broad scan or side-effect probe is authorized by this record.
