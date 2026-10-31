# S12 — ordinary torrent-material backend slice

Base: `760720ed0a96fd9e7a647914c14545bde5ef857e` (the locally accepted S10 commit; its remote delivery remains pending). Fixed MoviePilot source: `8a83efeacd3e26b82be495e17d50146e31c96a60`. Delivery branch: `plugins-refactor`; no main merge or deployment.

## Usable scope

`plugins.SharedDownloadChain.DownloadTorrent` calls the original-style `DownloadChain.download_torrent` operation through a declared standard SDK capability and the real App resource dispatcher. It returns distinct None/string/complete-byte values, folder and ordered file names. It preserves descriptor GET/POST query behavior, source form-field order, finite source exception categories, failure messages, configured-site authority, actual persistent torrent cache and same-worker recovery. It does **not** submit a downloader task or operate media files.

A private invocation-owned protocol transfers raw bytes in 1 MiB pages, verifies total and SHA-256, and releases the object. Site/proxy authority is checked during acquisition; subsequent immutable pages still require the same live invocation/identity/generation. The ordinary serial worker keeps the already-established accepted-call bounded-drain contract: caller disconnect is not immediate cancellation of its admitted exchange. No concurrent-worker policy was changed.

## Actual evidence

- Selected unchanged original source bodies: 19 wrapper observations (17 tuples and 2 exceptions), 3 non-string/log-order cases, 301/302 missing-Location exceptions, form-key ordering, and the trailing-separator parser counterexample. Source dependencies are explicitly controlled recording ports; this is **not** full original HTTP/cache execution.
- Target component/SDK differential gates, actual owned HTTP/TLS transport normal 13 and race 2, and invocation transfer gates. Earlier evidence is inherited only for its unchanged finite paths; it is not described as a fresh whole-tree run.
- Exact release candidate: real installed worker/App normal 4, host race 2, plus one existing S10 retained API/job/self-remove regression with its previously accepted worker. This race gate instruments the host, not the normal worker binary.
- Two actual normal server processes, PID 23 then 58: 13 material-query scenarios (30 HTTP API requests overall), one installation, failure then recovery, persistent cache/notification readback, distinct cold worker, zero cold material refetch, and both natural exit 0. The worker really reconstructed two sequential 16 MiB results (33,554,432 bytes); callback counts are not measured physical wire-byte counts. No proxy/CA/environment rewrite, CONNECT, forwarding or real site credentials.

Original parser/App01/App02 failures remain failures. The App harness fixes account for the existing SiteUpdated setup GET, ordinary public error code, canonical log projection, and serial drain behavior; they do not hide product failures or rewrite their old logs.

## Limits and remaining work

The existing **16 MiB per-material resource guard** is retained. The source has no identical cap; arbitrary-size equivalence is not claimed and this is not a blanket approved adaptation. Unsupported response text-decoder boundaries also remain explicit. Approved certificate compatibility applies only to the independent material client; cross-origin Cookie/Authorization/custom-token protection remains, and system/Git/browser TLS is unchanged.

S12 browse/RSS and its S14 dynamic-page dependencies remain open. This slice adds no UI; it does not establish browser acceptance, real tracker/downloader/NAS behavior, business-plugin migration, recognition quality, all S12/platform completion, CI success, or remote delivery.

## Provenance / reproduction

`publication-manifest.json` binds the exact product, ledger and selected evidence files. `evidence/` contains original results, exits, source outputs, test JSON and input bindings; `normal-cli/` retains non-secret original responses, server logs and the exact tool-command receipt. `review/` contains independent finite decisions. Selected fixed source files and the actually executed collectors/runners are retained unchanged, including their original environment paths; do not mistake them for portable scripts that can be executed against arbitrary services.

Rebuild in an isolated authorized Linux workspace with the repository Go module and native dependencies. Use the exact test patterns in the retained `result.json` files, build the normal/denied owned fixture, and bind `OWNED_TORRENT_MATERIAL_WORKER`/`OWNED_TORRENT_MATERIAL_DENIED_WORKER` to those exact programs. The CLI driver requires its exclusive owned layout, exact `http://127.0.0.1:39123` controller and verified program hashes. It must not be changed into a generic proxy bypass. Private bootstrap data and executable binaries are deliberately excluded.

The full development raw evidence, including omitted duplicate input maps/large Go-list output and prior failure snapshots, remains locally retained. Earlier development checkpoints are externally saved (latest pre-release checkpoint `libfile_663ec93c9b508191af0eed3f3d06945b` v0); that checkpoint predates the release-tree/CLI execution. The selected current release evidence is included in this publication, not retrospectively claimed present in the earlier backup. The manifest records each omission's original hash. No missing old cloud evidence is claimed restored.

## Ledger structure check

The official non-final checker was actually run. Its pre-existing 664 global structural/binding issues remain unchanged. The 19 new S12 binding-format issues found during publication were repaired using exact original input/result copies; the repaired error list is identical to the baseline (0 additions/removals). `structure-check/` preserves all three actual outcomes. This is not a green global ledger or final-project certificate. Current product/input/result hashes are independently checked by this slice’s manifest and review.
