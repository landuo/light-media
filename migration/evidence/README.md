# Archived migration evidence

This directory contains source captures, logs, and review probes retained as
evidence. The Go probes are overlays for `internal/app`, rather than independent
application packages. The nested module boundary keeps root `go test ./...` and
`go vet ./...` from compiling archived probes as standalone packages.

All original probe bytes, evidence hashes, and product tests remain unchanged.
To reproduce an archived probe, use its recorded overlay and verification inputs.
