# Plugin framework: supervised runtime and Reset milestone

This task-branch milestone adds production runtime claim supervision and a bounded Reset fence/cancel/join path. It is not the completion of the entire plugin framework. Built-in implementation update/version migration is the next vertical; external plugin repositories and a marketplace are not implemented here.

## Production contract

- Generic and existing BrushFlowLanduo, SeedRescue, and Lark claim paths reserve an instance-bound execution ticket and journal the exact attempt in the claim transaction. Plugins do not construct clone IDs or implement cancellation registries.
- The executor activates the real ticket before SDK admission and retains it through deferred native work, durable result persistence, and cleanup. The ticket is released only after exact completion readback. A context cancellation alone is not proof of completion.
- Reset closes admissions for the exact instance, cancels captured executions, and joins outside SQL transactions and App lifecycle locks. Existing business-owner reset hooks remain in place. The final reset transaction verifies the same private instance/version/generation capability.
- Committed claims that provably never entered execution can be finalized by the framework. Active, uncertain, or cold unresolved attempts are not treated as completed. Reset rejects without clearing configuration when quiescence cannot be established; a quarantined transition is not reported as success.
- Reset does not cancel another instance. HTTP authentication remains at the existing App routing boundary. Tests delete only assistant-owned synthetic temporary data.

## Verification and retained evidence

The final production bytes passed 46 focused roots in ordinary and race modes, including real SQLite claim/rollback/cold evidence, CategoryEditor with an actual SDK process, and authenticated Reset against a blocked local HTTP operation. The network vertical covers both a lightweight real AutoSignIn SDK process with race instrumentation and the actual ordinary cmd/server worker under a race-instrumented host. It verifies cancellation, cleanup join ordering, unknown-outcome preservation, and a second instance completing independently.

The first necessary adjacent run exposed two old test helpers bypassing the now-required execution/admission boundaries. Only those test helpers were corrected: one now activates/releases the real claim around its existing SDK/native work; the other routes Reset through the actual common outer handler. Production bytes and functional assertions were unchanged. The original three failing roots are retained, not erased. The corrected exact ten adjacent roots passed in ordinary mode (6.240 s) and race mode (64.399 s). Vet passed for internal/app, internal/plugins, and internal/plugins/builtin; the actual cmd/server build passed. All 5,472 candidate source files matched their frozen SHA-256 before and after these gates. No CI run is claimed.

Two unrelated unresolved observations remain explicit:

1. The earlier full App-test-race SDK fixture timed out waiting for the first local site request, before Reset was invoked. A single instrumented reproduction did not reproduce it. The added actual-server and lightweight-SDK coverage does not establish the old failure's cause.
2. The preceding framework stage retains a grouped NoticeV2 race timeout in two leaves. Candidate diagnostic and exact-main single-root passes did not explain that failure. This milestone does not claim that the complete prior 355-root race inventory is green.

## Boundaries

Linux and local storage remain the supported scope. No user production media, credentials, external NAS, or repository deletion is involved in this validation. This is a separately reviewed task-branch stage, not an aggregate-branch release or a claim of complete clone/reset/update acceptance.
