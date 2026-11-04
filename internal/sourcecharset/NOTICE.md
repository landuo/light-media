# Source attribution and Go changes

This package is an independent Go translation of the fixed default detector from Charset-Normalizer 3.5.2 (TAHRI Ahmed R., MIT). Its literal language/range tables and candidate order are mechanically preserved. Complete MIT notice is in LICENSE.charset-normalizer.

The Unicode 15.0.0 properties, one-codepoint lowercase/decomposition data and contextual-lowercase observations come from the fixed CPython 3.12.14 runtime; character flags also apply the original CN classification function. The homogeneous-float sum and reachable small-candidate stable sort are translated from CPython v3.12.14 Python/bltinmodule.c and Objects/listobject.c. The runtime's complete PSF license text is preserved as LICENSE.PSF; Unicode data notice is LICENSE.Unicode (reused unchanged from the existing decoder component).

Source locations: https://github.com/jawah/charset_normalizer ; https://github.com/python/cpython/tree/v3.12.14 . Exact source, runtime, algorithm table and actual original observations are bound by the migration evidence, not inferred from these links.

Changes: Go data structures/error returns; immutable embedded tables; per-invocation coherence state and bounded-by-codepoint cache; the original default algorithm only, without Python or native-library runtime requirements. The public result retains encoding presence separately from an empty encoding. Detection does not call the existing Chardet detector. Codec decoding reuses the separately attributed existing encodingdetector implementation. Optional original CLI, file-opening API and configuration knobs are not exposed by this consumer-specific package. Hash-based submatch factoring uses actual decoded-string equality; random hash-collision behavior is not claimed to be reproduced.

These notices accompany source redistribution; this document does not assert behavioral acceptance or change the scope of any license.
