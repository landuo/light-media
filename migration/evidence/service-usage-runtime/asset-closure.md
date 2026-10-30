# Static deliverable asset closure

Reviewed current candidate `bd30bc25…a16279a87`, without build, JavaScript execution, browser, or network access.

- Current embedded distribution: **358** paths, all matching candidate hashes.
- Git-tracked distribution at the pre-stage review snapshot: **194** paths.
- Current emitted distribution: **111** paths, including **47** not already tracked.
- Older untracked/non-emitted paths: **117**, retained locally and not required for the selected delivery.
- Starting from current `web/dist/index.html` → `assets/index-DNRPL9b-.js`, the conservative filename-occurrence graph reaches **134** files. It includes every known filename in a reachable file, including all Vite preload-table entries even if a particular entry is not used. There are no duplicate embedded basenames. Thus it deliberately over-approximates ordinary literal imports/references rather than dropping lazy routes.
- **0** reachable files are among the older 117; **0** lie outside tracked ∪ current emitted. The 24 reachable-but-not-emitted files are existing tracked `plugin_icon/*` images. Current `version.txt` is not referenced by this entry graph; its inclusion in emitted output is harmless.

Supplemental quoted local-path scan produced 109 occurrences: one `/favicon.ico` used with the selected site's `t.origin` (site favicon fallback, not a missing fingerprinted distribution import), three HTML-worker and 103 JavaScript-worker Browserify internal module-map references, plus two YAML-worker internal module references. Their surrounding source is retained in `asset-closure.json`. These worker references are definitions/dependency mappings/`_dereq_` calls inside self-contained bundled modules, not browser requests to the apparent build-machine paths. None references any of the old 117 assets.

**Conclusion:** the proposed tracked + current emitted selection contains the current entry's conservatively identified bundled UI dependency closure. There is no static requirement to upload or delete the old 117. This is not a runtime request trace or a proof about arbitrary future plugin-supplied URLs. The frozen tested binary embeds additional historical files; a clean rebuild from the selected Git assets is **not claimed byte-identical** to that binary. The finite assertion is equality of current referenced UI asset bytes/closure, not identical entire embedded filesystem or binary.

Detailed graph: `asset-closure.json`, SHA-256 `fd9c68036cf076c7786e4110b22e1513f6ff213684141032489c6615b282bbdf`.
