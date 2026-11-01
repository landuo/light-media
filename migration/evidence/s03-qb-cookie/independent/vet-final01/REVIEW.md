# S03 Cookie07 — separate static vet acceptance

**ACCEPT: exact two-package go vet gate.** Recorded session70132 / 1b6d65→45d845 exited0 at 07:28:22.680310 UTC; stdout and stderr are empty. The command in `result.json` selects only `internal/plugins/downloadercommands` and `internal/plugins/iyuuautoseed/native` with the frozen gates02 overlay. No test or HTTP/root was run by this static gate.

Its 2,829-input before/after maps equal the native normal02 terminal map. All map paths and the three additional inputs (actual vet tool, native map file, independent preexec seal) independently rehashed unchanged. Vet binary SHA is `de9894a135bca12beaec965b062016bf7f3bc708b36513817c50f218a2930040`. The recorded result hash matches the coordinator tool receipt. No source/Go/network rerun by this reviewer.

This is separate from native normal/race: their original compile commands had `-vet=off` and remain accurately NOT_RUN for vet. Their inherited virtual-only reason was corrected by the retained clarification, not by fabricating a current failure or rewriting the original result. This narrow success is not whole-project vet, full platform acceptance, or an additional business/runtime gate.
