# Original zhconv delta03 — finite actual ACCEPT

The coordinator's sole execution `c0170c` exited 0 (no session, reported 0.184471427 s); all five fixed strings returned strings, stderr empty. Receipt is explicitly a coordinator transcription, not a new raw-tool replay. The 26,950-byte stdout SHA is `f70aba727c8f4ab6f49f50206933d0355bda26dfb77e2c8443da3157d5c46575`.

Independently matched all five input objects against the admitted case file, validated each input/output SHA, output UTF-8 length and strict base64. Rehashed all 84 current inputs and all 42-before/44-after module file bindings without drift. Manifest, exact initializer/extension, command and import surface are unchanged from admitted delta03. Audit counts 3 package-import / 1 initializer-exec / 129 readonly opens / 1 refused-unbound-pyc match the inherited narrow mechanism; this is not a tool denial or native syscall trace.

Actual source results:

| Selected input | Actual result |
|---|---|
| Correct adjacent add→remove→add | `OwnedOwned` |
| Correct adjacent remove→add | `OwnedOwned` |
| Remove builtin replacement `专著` | `專著專著` |
| Remove builtin source `專著` | `专著专著` |
| Remove long secondary amid short/long overlap | `专著专著` |

These match the inspected original dataflow: removals remain accumulated regardless of later adds; builtin removal checks replacement text and skips the first source rune of a removed longest match; a surviving shorter secondary does not outrank a longer builtin. This is inference connecting exact observed inputs to original code, not arbitrary rule coverage.

The original 4,789-case raw, malformed adjacency input, panic and raw D ordering remain immutable. This increment does not rerun or normalize them and contains no D cases. Target matching still requires its own bounded evidence. No reviewer import/native/Go/network execution, host acceptance, full name equivalence or new dependency/API authorization.
