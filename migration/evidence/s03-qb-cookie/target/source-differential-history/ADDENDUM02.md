# Preparation02: exact candidate03 rebind

Preparation01 and all its original bytes remain frozen as the checkpoint02 static-mismatch history; it was not executed. The same exact test (53de9886…) and original raw (758df1e9…) are now bound to target owner candidate03. Four-port signatures and three other leaf files are unchanged. Only qb_cookie_parse.go adds the original key/attribute diagnostic spelling (44d97175…); this statically addresses the known `Illegal key 'owned@bad'` trace mismatch. It does not demonstrate a runtime PASS or acceptance of broader parser behavior.

The test still compares every original log exactly; no assertion, raw byte, case, count or projection was weakened. Coordinator may use the current preparation02/overlay-candidate03 after independent preexec; preparation01/overlay-checkpoint02 remain non-executed historical inputs, not a failed runtime. No Go/source/HTTP execution or ACTIVE/owner edit occurred.
