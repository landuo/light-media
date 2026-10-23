# Plugin Form projection facade

Current implementation contract. See plugin-framework-stage-one-status.md for
the exact acceptance boundary and unresolved NoticeV2 regression. This facade is
not a native-code sandbox.

## Exact public surface

Both PluginDefaultsTransaction and the actual pluginDefaultsTransaction value
and pointer expose exactly:

- Data(context.Context, string) (PluginStoredValue, error)
- SaveData(context.Context, string, int64, any) (PluginStoredValue, error)
- Config(context.Context) (PluginStoredValue, error)
- VerifyInitialization(context.Context, PluginInitializationProof) error

There are no Exec/Query/QueryRow methods, Store or Tx getters, Commit/Rollback,
or PluginProofReader getter. The latter remains a framework-internal helper
interface but is no longer embedded in or implemented by the callback facade.
Trusted App code in the same Go package can still capture a Store or access
private fields; neither this interface nor Form guards claim otherwise.

The only production constructor is the shared transitionInstanceConfig path,
used by both missing-default projection and full Form projection. It binds the
same transaction, store, current instance identity, implementation version,
generation and installed/enabled flags after framework admission. Existing
config CAS, default/Form declaration gates, busy checks, data admission,
propagation and sticky Form checks remain in place.

## Typed reads and proof semantics

Config queries this transaction's actual current installation and config row.
It checks the bound instance/plugin/version/generation, installed/enabled flags,
verifies instanceMetadata (including clone provenance), unseals config AEAD,
and requires an object. It returns isolated bytes including updated_at. A
callback's change.Next bytes are not proof and the pool/cache is not consulted.
Paused/disabled installations are valid if they match the admitted snapshot.

PluginInitializationProof contains only RunID, Action, Method,
InitializedRevision, InitializedConfigSHA, OriginRevision, OriginInputSHA,
OriginConfigSHA, OriginAckSHA and OriginRawSHA. Instance/plugin/version/
generation cannot be supplied in the public request; they come from the bound
installation. The verifier:

1. Requires init/reload, nonempty run/method, OriginRevision >= 1,
   InitializedRevision >= OriginRevision, and well-formed SHA-256 digests.
2. Revalidates the current bound installation/config and instanceMetadata in
   this transaction. It does NOT compare InitializedRevision or initialized
   config SHA to today's Next config revision or bytes.
3. Reopens the exact run, checking bound identity/version/generation, action,
   initialized revision, and running/succeeded state.
4. Calls readDeferred and requires a confirmed receipt, exact plugin/version/
   generation/action/method/origin revision/input SHA/config SHA, exact Ack and
   Raw hashes, and json.Valid(Result). readDeferred additionally binds run and
   instance, schema, AEAD, strict fields, and receipt outcome invariants.
5. Rejects every retained successor with from_revision >= run.ConfigRevision.
6. Uses verifyRunConfigChainTx from the original receipt to the run head and
   requires the resulting hash to equal InitializedConfigSHA.

Only an error is returned, never receipt bytes or a SQL reader. No business
PluginID special-case is added. A post-Form current config may legitimately be
newer than the immutable initialized head. Missing/corrupt proof data maps to
PLUGIN_RUN_PROOF_INVALID. Context cancellation/deadline, sql.ErrTxDone and SQL
operational failures preserve their actual error identities.

## Lifetime and failures

All facade copies share private active/failure state. Config and typed proof
failures latch locally, so a projector cannot swallow them and commit. Narrower
caller cancellation/deadline failures also latch, including from Data/SaveData;
a live parent context cannot turn a swallowed canceled journal write into a
committed Next config. Neither typed method is mandatory for projectors without
an initialization-origin need. Ordinary Data missing-row errors are not fatal.
SaveData CAS/JSON/admission rules stay intact.

The framework revokes the facade after the synchronous projector returns, also
on panic. Short mutex sections cover only active/failure state, never callback,
MarshalJSON, database work, or any wait/drain. Database operations use contexts
rooted in the original callback, also honoring the supplied context's
cancellation. The caller's earlier deadline is installed directly on the bound
context. AfterFunc forwards cancellation, but never converts deadline expiry
into Canceled. A replacement Background cannot extend the original deadline or
erase its Form scope. SaveData retains and synchronously checks the original
caller context again after custom MarshalJSON and before SQL, independently of
AfterFunc scheduling. All four methods check original context errors on return,
retain cancellation/deadline identity, and latch those failures. Revocation
cancels in-progress facade operations cooperatively. This does
not authorize background goroutines and does not claim isolation from arbitrary
same-package native code or a malicious concurrent SQL writer.

Hand-built wrappers lacking the verified snapshot and lifetime fail closed.
Same-package fixture construction is now:

    installation, err := p.pluginLifecycleInstallation(ctx, rawTx, instanceID)
    // Check err before continuing.
    facade := newPluginDefaultsTransaction(ctx, rawTx, p, installation)
    defer facade.revokeProjection()

This is private test/host construction, not a new public authority-granting API.

## MergeMessageNotify mapping (read-only guidance, no Merge files edited)

In owner-v11-runtime-source/internal/app/plugin_merge_message_form_host.go,
mergeMessageFormProjector should replace:

    anchor, err := p.mergeMessageStateAnchorTx(ctx, tx, change.Installation)

with:

    anchor, err := tx.Config(ctx)

Keep the existing change.Installation equality, Next/Previous monotonicity,
anchor-vs-Next bytes, journal CAS and state equality checks. For the old
verifyMergeMessageStateAtConfigTx call, keep its business-local non-SQL checks
against change.Previous exactly:

    r := current.record
    previous := change.Previous
    if r.Instance != in.InstanceID || r.Plugin != in.PluginID ||
       r.Version != in.Version || r.Generation != in.Generation ||
       r.ConfigRevision != previous.Revision ||
       r.ConfigSHA != deferredInputSHA(previous.Value) ||
       !mergeFormValueEqual(r.Stored, previous.Value) ||
       r.ConfigRevision < r.InitializedRevision ||
       (r.ConfigRevision == r.InitializedRevision &&
        r.ConfigSHA != r.InitializedConfigSHA) {
        return mergeMessageStateInvalid()
    }

Then replace its raw-reader origin step with:

    err = tx.VerifyInitialization(ctx, PluginInitializationProof{
        RunID: r.InitializedRun,
        Action: r.Action,
        Method: merge.ConfigureCapability,
        InitializedRevision: r.InitializedRevision,
        InitializedConfigSHA: r.InitializedConfigSHA,
        OriginRevision: r.OriginRevision,
        OriginInputSHA: r.OriginInputSHA,
        OriginConfigSHA: r.OriginConfigSHA,
        OriginAckSHA: r.OriginAckSHA,
        OriginRawSHA: r.OriginRawSHA,
    })

Handle the returned error with the existing business proof error mapping where
appropriate. Do not pass change.Next as proof of the older journal anchor and
do not require InitializedRevision == change.Next.Revision. Standalone trusted
non-Form business readers may retain their private raw-reader path.

The hand-built wrapper in Merge form_host_test.go should use the private
constructor example above (or its verified existing installation snapshot),
not a zero-snapshot struct literal. The Merge coordinator owns these edits and
all current/future Go execution; its frozen input was not modified here.

## Regression evidence retained and added

Existing SQL assertions are retained in the SAME transaction through the
_test.go-only pluginProjectionSQLForTest helper:

- plugin_config_form_projection_test.go: original lines 113, 336, 340.
- plugin_form_session_test.go: original line 442.
- plugin_lifecycle_mode_test.go: original line 609.
- plugin_form_common_projection_guards_test.go: original lines 226, 371, 375.

Three hand-built dispatch-boundary wrappers use a same-TX verified fixture
constructor. No synthetic_projection table is introduced or assumed.

New tests use actual SQLite and PluginStore AEAD with clearly synthetic run,
receipt and transition rows. Coverage includes value/pointer method sets,
unbound wrappers, same-TX Next and no aliasing, foreign instances, lifecycle
and clone metadata drift, foreign ciphertext, non-object config, historical
initialized heads, init/reload and running/succeeded, every typed field,
receipt/run mutation, strict/missing/corrupt receipts, missing/corrupt/successor
chains, swallowed failures and durable rollback, cancellation/deadline/TxDone/
SQL error preservation, and revocation inside custom JSON serialization.
Additional deterministic cancellation regressions withhold AfterFunc delivery:
MarshalJSON cancels a narrower caller or waits for its actual deadline event;
both must reject before SQL and roll back Next plus earlier journal writes even
when the callback returns nil. A separate context test verifies the earlier
caller deadline and DeadlineExceeded identity without sleep or polling.

## Acceptance boundary

See plugin-framework-stage-one-status.md and the accompanying verification JSON.
The typed facade tests are included in the completed local ordinary/race evidence;
the separate NoticeV2 group failure remains explicitly unresolved. Real reset/join
and compiled-version migration are not implemented by this API.
