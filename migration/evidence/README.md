# Archived migration evidence

This directory contains source captures, logs, and review probes retained as
evidence. The Go probes are overlays for `internal/app`, rather than independent
application packages. The nested module boundary keeps root `go test ./...` and
`go vet ./...` from compiling archived probes as standalone packages.

The ledger preserves the original evidence paths and hashes. Some raw outputs
for accepted features are now available only in Git. To recheck the historical
ledger, use its complete snapshot at `91d64279e1e75b0196267d8b4c8203bfbca9cc97`.
For a single archived file, use `git show 91d64279:<repository-relative-path>`.
The replay probes and regression test inputs remain in the current tree.
