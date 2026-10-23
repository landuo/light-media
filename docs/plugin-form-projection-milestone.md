# Trusted full-config Form transaction milestone

This backend milestone adds `ProjectInstanceConfig` and the separate compiled `config_form_projection: transactional/v1` declaration. It does not complete the plugin lifecycle framework or expose a production Form adapter.

## Behavior

- A trusted, non-nil host projector and an exact compiled declaration are required. Worker JSON cannot grant this authority.
- Full replacement can delete existing keys when the trusted source-derived transition requires it. Every actual source Save advances revision and invokes the projector, including byte-equal saves.
- The same transaction preserves instance/generation/revision checks, authenticated metadata, native/reference gates, active-run/deferred busy rejection, encrypted config, pending snapshot/proof propagation, and atomic owner-data projection. Failed projection or propagation rolls back the entire step.
- Existing missing-only normalization and read-only no-op behavior remain unchanged. Immutable config-save origins and deferred receipts are not rewritten.
- Business adapters must independently authenticate the previous journal and recompute the requested transition. The framework does not authorize arbitrary worker replacement, manufacture initialization evidence, or combine a source sequence of saves into one transaction.

## Verified

The frozen 5,431-file isolated candidate passed 35 selected root tests, 127 tests including subtests, in both ordinary and race modes. Complete input SHA-256 checks were unchanged after both runs. Coverage includes replacement/deletion, byte-equal saves, Config/Stored separation, alias isolation, rollback, stale identities, undeclared permission, busy receipts, and unchanged origin authority.

Positive transaction tests are mechanical tests using a real existing compiled plugin declaration through the private common transaction. No production manifest declares the new permission yet; a real production adapter must be integrated and validated separately.

## Publication boundary

The five Go files are applied on top of `1ac6924158b373836780a8f017279f941c644fd7`. That parent added four test-only uninstall/Transfer fixture files after the isolated V8 snapshot was made; those files remain untouched. The isolated test results are not represented as a combined-branch regression. The aggregate branch must still validate the combined implementation and real browser entry points.

No frontend, Merge business adapter, event admission, Form-mode resolver, runtime cancellation registry, reset unification, or update/version migration is included. The original uninstall App-race red remains retained and is not retroactively called successful. See the companion verification JSON for exact source hashes and results.
