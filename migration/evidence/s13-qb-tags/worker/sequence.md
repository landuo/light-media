# Coordinated finite CLI plan — not execution evidence

This additive note supersedes only the illustrative five-business-call grouping in `CONTRACT.md`. Candidate01/main/manifest bytes remain unchanged. The CLI author and worker author coordinated the following seven calls; the driver itself and its safety gates require separate independent review.

1. Delete `owned-label`: one current callback.
2. Delete `owned-fail`, then explicit delete `owned-label` with continue-on-error: two current callbacks. First `removeTags` succeeds, second/global `deleteTags` returns owned 500; subsequent operation is distinct, not replay.
3. Lookup tags `owned-found`, status null: one current callback, one real source 3-second sleep, info then cleanup.
4. Lookup tags null, status null: one current callback; peer `[7]` yields direct AttributeError. Successful callback envelope is distinct from failed typed SDK operation.
5. Lookup tags `owned-empty`, status null: one current callback; native nine 3-second sleeps plus nine actual owned 500-ms delayed empty-info replies. No worker delay, iteration or timeout bypass.
6. Delete `owned-label` plus previous-caller probe: one current callback and one separately rejected old callback. Same worker after the long lookup; stale probe requires exact identity denial and zero peer effects.
7. Cold second host/worker, same persistent owned configuration: delete `owned-label`, one current callback, no reinstallation/refetch.

Arithmetic: **seven business calls, eight current SDK callbacks, one old probe; nine callback attempts in total**. An initial coordination message called this nine current callbacks plus one old; direct inspection of the proposed operations exposed that count error before execution and the CLI author was notified. No extra call was added to make the mistaken count true.

Expected owned peer totals in the prepared CLI are version 12, info 11, removeTags 6, deleteTags 6 (one owned 500), plus index/worker one GET each: 37 requests. These are planned finite observations, not actual receipts or real daemon effects. All lookup statuses remain null; peer phase is private controller memory, never a hidden worker or product parameter. Only the exact slow-empty control frame may use the separately reviewed controller response wait; worker stays ordinary serial Serve with one SDK invocation.
