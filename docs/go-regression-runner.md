# Reproducible root-module Go regression

Use Node.js (built-in modules only) and the Go toolchain from `go.mod`. No Python runtime, npm installation, production service or live media is required by this runner.

```sh
node scripts/go-regression.mjs plan       # audit recipes/platform/provenance, no builds
node scripts/go-regression.mjs preflight  # build real SDK fixtures and server only
node scripts/go-regression.mjs run        # preflight then every root-module test root
```

Run only one full suite at a time after other editors finish. Builds and tests use `-p=2`, the existing default Go caches, and an exclusive canonical OS temporary directory. On macOS this is `/private/tmp/light-media-regression-*`, not a checkout path: `/.worktrees/` is intentionally rejected by production transfer hidden-directory rules. Do not change those rules to make tests pass. Evidence is retained in `work/go-regression/<timestamp>-<pid>/`; no user files are removed. After reviewing/preserving evidence, clean up only the exact owned temporary directory recorded in `preflight.json` (not a broad `/tmp` glob).

## Evidence and scope

* `preflight.json`: commit, dirty status, source digest, toolchain/platform/cache locations, all discovered owned binary consumers, missing recipes, and source/browser prerequisites. Unset provenance inputs are **unverified**, never successful substitutes.
* `build-*.jsonl`, `.stderr`, `.result.json`, `builds.json`: actual commands, exit status, binary hashes and supported linker variants. Identical recipes share one binary; different variants never share a cached artifact by filename alone.
* `environment.sh`: exact owned fixture bindings and safe TMPDIR for a focused diagnostic test. Do not use these bindings for production.
* `list.jsonl`, `expected-roots.json`, `shards.json`: `go test -list . -json ./...` is the authoritative current-platform root set. Each package is divided into at most 40 roots per invocation. Set `REGRESSION_SHARD_SIZE` to 1–200 to adjust the bound. Packages without roots still get a compile/test invocation.
* `shard-*.jsonl`, `.stderr`, `.result.json`: unmodified Go JSON, command and exit status for every shard, including failures/timeouts. Each has a 30-minute Go test timeout and 40-minute outer budget. A failed shard does not prevent later shards from running.
* `summary.json`: expected/observed root counts, missing/duplicate/unexpected roots, failures, all root/subtest skips and command statuses. A timeout never silently becomes a pass.

Exit 1 means a test/command/coverage-integrity failure. Exit 2 means execution completed but there are unresolved fixture recipes, skipped tests or coverage/provenance limitations. `testsPassed` only describes enumerated current-platform test execution; `fullyVerified` must not be inferred from it. Darwin excludes Linux-specific files by Go build constraints. These files are separately inventoried, not called passed. Linux source packages, browser assets and captured source oracles need their genuine input/provenance; the runner does not synthesize them. Optional environment-gated branches and browser UI workflows remain separate from passing root tests. Root `./...` also excludes nested modules and `testdata` Go ports, which need their own suites.

`testdata/go-regression-fixtures.json` supersedes the historical 45-entry fixture list for this runner. New binary prerequisites without a reviewed source recipe are reported explicitly. Never map an unsupported prerequisite to `/bin/true`, an unrelated worker or a fake shell success. No failing roots are filtered from enumeration or waived by the harness.

For a separately coordinated race run, use `REGRESSION_RACE=1 node scripts/go-regression.mjs run`; all owned fixture builds and test commands receive `-race`. This does not reuse ordinary fixture binaries. Audit-helper smoke tests are `node --test scripts/go-regression-results.test.mjs`. Builds probe `--manifest` for commands supporting it, retain the actual declarations, and validate source-stub omissions. `fullyVerified` remains false when external provenance prerequisites exist, even when paths were supplied: existence alone is not provenance attestation.
