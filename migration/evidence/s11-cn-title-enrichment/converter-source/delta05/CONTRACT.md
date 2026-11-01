# Native zh-hans delta03: five input strings only

The original 4,789-case run and candidate02 remain unchanged. Its `add-remove-add` input was literally `}-{-|`, not adjacent `}--{-|`; actual output faithfully kept literal syntax. It is not proof of the intended remove-between-adds branch. This five-string increment closes precisely that source-dataflow gap; it does not replace, rewrite or rerun the 4,789 inputs.

All five inputs are fixed UTF-8 strings, max < 128 B. The first two rules are assembled from complete explicitly separated token strings so the correct `}--{` boundary is visible. `專著`→`专著` and prefix `專`→`专` are static facts from the bound original Hans table; these are used to choose inputs, not as expected conversion outputs. The last input additionally exposes a secondary short key overlapping the builtin long key after removal of a secondary long key.

Only the already admitted exact `zhconv(text, 'zh-hans')` two-positional-argument call is used, default wikitext unchanged. No make_converter, custom path/file API, other public variant, native library rebuild, original app import, browser/network or Go execution. Driver differs from frozen preparation02 only in its own directory and hardcoded count/data-key count. Original import/guard/output/exception behavior is identical, including denying unbound bytecode before falling back to the exact initializer.

Every expected value is deferred to the coordinator's real native outputs. No expected value/status is supplied or asserted. Raw strings, native exception class/message, stdout/stderr/exit are retained as-is. No D ordering normalization is introduced. The original 4,788 return + one PanicException run remains a separate fact; these five new calls are NOT executed during preparation.
