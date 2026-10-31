# Transport zero-write clarification — immutable supplement

This supplement supersedes any overly broad “no selected request replay” / “single POST attempt” wording in native candidate01, its CONTRACT and later summaries. It does **not** change any source, existing candidate binding, prior observation or acceptance result.

## Exact static input

- Pinned toolchain version file: `/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/VERSION`; contents: `go1.26.0; time 2026-02-10T01:22:00Z`.
- Actual implementation: `/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/src/net/http/transport.go`.
- Full-file SHA-256: `1c170ec3581321bd19ccd1b15863f46b16dc749ac6d54d00f5e97f7ffa2ccb5f`; 100272 bytes.
- Relevant original lines: `persistConn.shouldRetryRequest`, **818–857**, specifically fresh-connection rejection **836–841** and `nothingWrittenError` branch **842–845**.
- Candidate manifest remains SHA-256 `43a01b8cc022775b2d6969e34317570f292d9a98fd969b26465f326bb9132c7b`; its three current source files were rehashed unchanged for this supplement. No Go execution, network, new probe, vendor import or product edit was performed.

## Precise guarantee and limit

The new fixed native commands perform **no application-level replay of a selected mutation**. The inherited private dispatch wrapper removes the request's `GetBody` replay callback. This is not a guarantee of exactly one internal transport attempt for every body shape.

In the pinned Go implementation, a request on a reused connection may be retried when the transport reports `nothingWrittenError` and the outgoing body length is zero, even with no `GetBody`. Source-faithful qB None selectors can produce that zero-byte form body for start/recheck. This particular path records that no bytes were written; it is not evidence of an unknown-effect mutation being replayed. This finding is static, not an observed retry in an executed fixture.

The native dispatch marker is deliberately conservative: once it delegated a selected request to the transport, a subsequent failure remains an unknown-effect error. This supplement does not create a new retry grant, infer a safe external outcome from an error string, or weaken the one-use upload claim/tombstone.

Prepared/actual owned-peer tests can assert only the requests observed by their fixed peer and the explicit no-application-replay branches. They must not be described as proving a universal single underlying transport attempt. No transport, keep-alive, wire-body, environment/proxy or global policy is changed to suppress this known zero-write internal behavior.

Final publication wording must use these qualified statements, retaining the source-constructor/TLS and other pending boundaries separately.
