# Original plugin-module port: installation and native object slice (WIP)

This is a partial checkpoint against MoviePilot backend 8a83efeacd3e26b82be495e17d50146e31c96a60, not full module acceptance. Other plugin development remains paused. main and dev/plugins-continuation are not changed by this task-branch checkpoint.

## Included behavior

- Existing imported-class install appends installed membership, then executes original reload and registration. The already-present non-force branch does not consume release_version. Missing repository/unsupported acquisition is not reported as a successful install.
- The existing Go market POST can select an exact already-bundled compiled artifact. This is a Go transport adaptation, not a Python repository download. Unknown requested bundled versions fail without installing. First initialization receives missing configuration as None/null; no Form defaults are saved on the user's behalf.
- Original manager initialization has an exact host-owned object/run binding. The shared host, deferred-call and schedule admission paths recognize its legitimate revision-zero absent configuration. Other invocations retain their current identity, run, generation, configuration and permission checks. Bad ciphertext is not absence. Native AutoBackup receipts permit revision zero only for init and remain cross-checked with the sealed deferred admission and exact stored run.
- AutoBackup's original loaded object retains the actual native machine. Direct init reuses it; no additional warm init is inserted. Its actual get_state/get_service drive public registration. Public cron service registration is separate from the private init-created once scheduler.
- Actual private Stop removes eligibility only for that instance/generation's AutoBackup.once. It does not clear the central service group or a sibling. SQL failure leaves the private scheduler retryable; logging after invocation does not dereference a stale/nil active-call callback. This is owned synthetic testing, not a claim that running real backup I/O has been canceled or joined.

## Evidence and limitations

The v26 frozen source ordinary and race batches each passed 34 root tests / 65 leaf cases with exit 0, processes reaped, no failed or skipped tests and no input drift. The race package completed in 294.147 seconds; no race detector finding was reported. Earlier v24/v25 failures are retained: absent configuration was blocked at common host/deferred joins; the original private scheduler cleared only local declarations, not persistent job eligibility. One test-only compile failure referenced a nonexistent manifest wrapper; it was corrected to the actual AutoBackup Manifest function without changing assertions.

Installation tests include actual SDK initialization, installed/class/running distinctions, no implicit config write, preserved sibling state and explicit original failure branches. New private scheduler tests use a fixed synthetic clock and no scheduler pump, backup action, remote DAV or external task. They do not establish a real-time guarantee about an original +3-second once job running between repeated init calls.

Still incomplete: repository acquisition and uniform executable resolution are in an independent unintegrated Go-artifact slice; persistent SDK sessions have separate protocol tests but are not yet integrated into the App; all-catalog standard methods, remaining real API/event/service bindings and the complete real-server/browser acceptance remain open. Arbitrary Python package execution is not supported. Unknown native host contracts or versions must not be enabled by relabeling a manifest. No complete-module or whole-project delivery is claimed.
