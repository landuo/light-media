# Owned service UI fixture

Test-only Go worker, never a production business plugin. `main.go` is a byte-identical copy of the source actually compiled for the official cloud-preview worker `OwnedInput` version 1.2. Its `run_service` handler is identical to the current `cmd/owned-input-fixture` handler; `handler-unchanged.json` records the comparison. The extra form selects synthetic tuple/list/error results and an optional task-owned loopback gate. Do not configure a production address.

The executed worker SHA256 was `b7750bd8234e42ca40ba4aa05841fae5611353afe010967d2a8413d65bbedcbf`. It was built once from the recorded source overlay; copying the identical source here is not a second build claim. Reproduce with the repository's Go toolchain and existing dependencies:

```sh
GOPROXY=off GOSUMDB=off GOFLAGS='-mod=readonly -buildvcs=false' \
  go build -o /tmp/owned-service-ui ./testdata/service-ui-20261030
```

Register only in a task-owned test instance. The official UI acceptance enabled three synthetic services, exercised the owned gate and ordinary failure/recovery, then disabled all three. No system service was manually run. UI list state does not expose the backend's numeric progress or detailed error fields; those were verified separately through the source-bound backend tests.
