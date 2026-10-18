Source and oracle provenance only. Nothing in this directory is imported or executed by the application.

The two original source files are byte-for-byte fixed NAS references. All 20 method ASTs match; clone metadata is separate. No original configuration/KV values were read.

Local oracle reproduction requires a reference-only fast-bencode 1.1.8 installation compatible with the local interpreter. Point TRANSFER_ORACLE_RUNTIME at its isolated import directory, then run generate_source_oracle.py and generate_bencode_oracle.py. Each writes expected JSON beside the script; it does not modify the application test fixtures. The original generation used Python 3.14.4/macOS arm64 and the fast_bencode-1.1.8-cp314-cp314-macosx_11_0_arm64.whl SHA256 43d448a44d72dd5e0430997fac1249c9b235ac28a938f1ef06334bdb2dd03b6e.

The C dependency has malformed-input crashes/uninitialized/overflow behavior. Those cases are marked explicitly and rejected by Go. No C undefined-behavior equivalence is claimed.
