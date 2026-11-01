# S11 explicit-ID App admission — checkpoint 01 (prepared only)

Observed repository HEAD `911846b9a0cfe61da96d948d6b170f90f098c214`.
Source MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`.
No main-tree edits, source imports, Go formatter/build/tests, services or network
were run by this block. This is an actual external implementation candidate,
**not compile-ready** until the separately owned SDK constant and final native
method exist; no stub or success/None placeholder is supplied.

## Exact owned paths / integration

- New `internal/app/plugin_shared_media_recognition.go` is held as `.go.txt`.
- New `internal/app/plugin_shared_media_recognition_test.go` remains absent;
  finite tests follow the native interface/independent review, not a mock claim.
- `resources.patch` proposes exactly two old-file hooks: allow the one capability
  under resources.v1 and dispatch after existing scope/identity/log attribution.
  Old `plugin_original_resources.go` is copied verbatim in `preimages/`.
- No changes to module discovery, retained lifetimes, SDK ABI, caller budgets,
  provider transport, global settings, other resource routes or the old UI path.

## Implemented first block

1. Strict 128 KiB ordinary JSON object `{args:{...}}`; exact eleven original
   argument keys, duplicate nested keys rejected, `json.Number` retained.
   This finite host representation requires valid Unicode scalar strings
   throughout; SDK-preserved escaped surrogate values do not become silent Go
   replacement characters. This is a declared boundary, not Python equivalence
   for arbitrary strings/objects.
2. `meta`/`share_meta` absent or null only. Reuse existing source-tested
   `originalCatalogRecognitionCallKwargs` for ID/source precedence and defaults.
   Omitted cache becomes true; explicit false/null remain unchanged. Admit only
   normalized TMDB positive int31 ID and mtype null/movie/TV. UNKNOWN is rejected,
   never collapsed into null. Other sources/types/IDs are explicitly pending,
   not a successful source None. This first finite domain is checked before
   discovery; it is not the eventual whole S11 input domain.
3. Existing current invocation validation and App admission/wg. Derive a private
   guard that revalidates current scope and exact TMDB credentials, proxy setting
   and image-domain snapshot. Nested calls retain the previous guard as well.
   No worker-selectable credentials/endpoint or unguarded fallback. Native must
   call `originalMediaRecognitionGuard` before/after each actual read, on cache
   return and final result; this helper is observation/authority, not transport.
4. Fresh context OR-inherits `!truth(cache)` before plugins/native; derived context
   has no global mutation and expires on return. This **does not implement cache**.
   Actual provider cache/fresh behavior is separately native-owned and pending.
5. Before discovery, only an actually loaded calling object declaring get_module
   requires existing S10 `originalManagerNestedTarget` and actual negotiated
   concurrent client. Serial self reentry rejects before taking methodMu, without
   silent skipping/new workers. Other serial workers are not blanket denied.
6. Existing real module discovery/invocation executes plugins first. Preserve
   None/tuple-empty, list accumulation/core execution/discard, falsey stopping,
   truthy source MediaInfo projection via existing full-model helper. This is not
   the higher name.recognize / image / Fanart path. A full object/null is enveloped
   in `{value:...}` under unchanged 16 MiB output bound and final current guard.
7. Meta-null report-payload early return is inherited from the actual original
   selected node; no report/share request is fabricated. No source name search,
   arbitrary MetaInfo construction or new metadata provider is implemented.

## Coordinated native contract / unresolved integration

Fixed production method, agreed with native owner:

```go
func (a *App) originalSharedTMDBIDNative(ctx context.Context, kwargs map[string]any) (json.RawMessage, error)
```

It must return a complete original MediaInfo JSON object or nil, not selector
get_info dictionary/compact catalog DTO. The native owner consumes
`originalMediaRecognitionFresh`, `withOriginalMediaRecognitionFresh`, and
`originalMediaRecognitionGuard`; these three helpers are defined only here.
Resource dispatch already supplies trusted plugin/instance/call attribution.
Native WARNING must map to the existing writer's WARN, without changing original
source level or global logger.

Native final implementation, raw provider/cache/augmentation/TV group behavior,
actual owned read seam and definitive source exception classification are still
pending. The previous UI wrapper blankets all native errors into None; this new
candidate deliberately does not do so for authority/pending/transport failures.
Exact definite-source-error reporting/catch must be independently reconciled
before runtime readiness. No SourceException envelope or fabricated cache success
has been introduced. Existing source `run_module` catches ordinary core errors;
this block does not claim all such error semantics are complete.

## Finite subsequent gates (not executed / not extra permission)

Wire/current-scope/default/cache truth and invalid input component root; actual
plugin-first/self-reentry/full-result plus native guarded read integration root
once the fixed helper exists. Actual worker gate must negotiate concurrency for
self get_module and demonstrate no blanket denial for unrelated serial targets.
Provider evidence must distinguish recording transport from real owned HTTP and
normal CLI. Keep old source10 and new selected source12 credits at their recorded
layers; neither proves actual provider cache or an end-to-end native model.
