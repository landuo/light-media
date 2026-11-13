# Independent source lifecycle test prerequisites

These four small fixture entrypoints were recovered byte-for-byte from the
owner's retained source-private-scheduler-drain-20261107 and
source-public-service-retention-optin-20261107 verification work. SHA-256 origins
are recorded in fixture-origins.json. They use the real independent standard SDK
and, for trace-asi, the actual AutoSignIn implementation from the separately
maintained github.com/landuo/light-media-plugins repository (GPL-3.0, retaining its
upstream notices). This host repository does not vendor that SDK or business
implementation. No Python runtime is introduced.

The producer source used in this verification is commit
6eb29963f4f201b759debcbbd297077bb173c520. Relevant cmd/auto-sign-in,
cmd/owned-stop-order, plugins/autosignin and internal/sdk paths were read-only;
related source status and hashes are recorded in `fixture-origins.json`. Ordinary real ASI
workers are never replaced with a host-linked fake. Trace entrypoints restrict
trace network access to loopback. All requests are test-owned synthetic inputs.

Portable build contract: require an explicit PLUGINS_ROOT pinned checkout, OUT
output directory, and FIXTURES pointing to these retained small entrypoints.
Construct Go overlay maps from "$PLUGINS_ROOT/cmd/owned-stop-order/main.go" to
public.go or legacy.go, and from "$PLUGINS_ROOT/cmd/auto-sign-in/main.go" to
trace-asi.go. trace-legacy.go overlays owned-stop-order/main.go. Only the entrypoint
changes; do not overlay or replace the SDK. Build with GOMAXPROCS=2 and -p=1:

| Environment | Package in PLUGINS_ROOT | Overlay | Link flags |
| --- | --- | --- | --- |
| SOURCE_PRIVATE_ASI | ./cmd/auto-sign-in | none | none |
| INDEPENDENT_PUBLIC_ASI | ./cmd/auto-sign-in | none | none |
| SOURCE_PRIVATE_ASI_TRACE | ./cmd/auto-sign-in | trace-asi | none |
| SOURCE_PRIVATE_LEGACY_TRACE | ./cmd/owned-stop-order | trace-legacy | none |
| SOURCE_PUBLIC_FIXTURE | ./cmd/owned-stop-order | public | none |
| SOURCE_PUBLIC_SERIAL | ./cmd/owned-stop-order | public | -X main.mode=serial |
| SOURCE_PUBLIC_LEGACY | ./cmd/owned-stop-order | legacy | -X main.mode=legacy |

The two plain ASI variables may identify the same one business executable. Every
variant must be independently compiled, not hardlinked to an ordinary or another
variant binary. Record manifests, hashes and inode identities. The legacy variant
advertises managed_public_services in the external manifest but deliberately does
not opt its actual worker manifest into the feature; the real SDK handshake must
reject it. The serial variant uses the real serial SDK Serve path; no skipped or
weakened admission assertions are needed.

Run with canonical owned TMPDIR and ordinary fixture variables already provided:
`go test -p=1 ./internal/app -run '^(TestSourcePrivateScheduler|TestSourcePublicService|TestIndependentPublicIndividualStopPreservesAcceptedSourceCallback)' -count=1`.
Missing sources/fixtures must fail runner preflight explicitly. Do not encode an
absolute developer-machine path in committed runner code.
