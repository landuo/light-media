# Plugin clone UI: bounded frontend milestone

This publishes the six frontend source/test files and their complete frozen production assets on top of the existing framework backend branch. It is not completion of the entire plugin lifecycle, runtime cancellation, version migration, external plugin repositories, or a plugin marketplace.

## Verified UI scope

The frozen frontend suite passed 213 tests, including 13 clone tests, followed by TypeScript checking and production build. The initial dependency-directory failure is retained in `plugin-instance-clone-frontend-verification.json`; that file is the historical build report and predates the browser acceptance below.

The actual `cmd/server` executable was exercised in assistant cloud Chrome on 2026-10-22, with a new synthetic local database and the compiled SystemProcess plugin. The browser verified suffix/name validation, cancel/reopen state, creation, server-side duplicate-ID conflict, reload persistence, inherited disabled configuration, independent saved source/clone settings, and clone-of-clone identity/metadata. The source refresh value remained 7, the first and second-generation clones retained 11, and all remained disabled. The actual server exited successfully and was reaped. Executable SHA256: `8ceb504f4c54a2e0d35875cf321092a0768f97975451c5a2c0d01d5c80097f4c`.

This browser result covers one safe built-in plugin and the exercised creation/configuration flow. It does not claim every plugin's runtime, broad reset/update lifecycle, every metadata boundary, or an all-repository acceptance.

## Rebase compatibility

The backend parent is `bc8c5b249f54ca17e5f14dfa827172457b371d9e`. These six frontend files were present byte-for-byte in the locally validated V10 source, but were **not previously published in that backend commit**. This milestone publishes source and assets together; it does not just replace the bundled index.

Static comparison of the V1 and V10 HTTP implementation shows the clone creation route is unchanged. The Form response retains `conf`, `model`, `revision`, and optional `render_mode`; V10 strengthens authenticated session/revision handling without changing that frontend wire shape. V10 ordinary/race validation includes real compiled cold Forms for CategoryEditor, SystemProcess, and MergeSiteSwitch. The earlier browser evidence is reused only for the unchanged frontend and exercised route, not described as a new browser run against the V10 server.

Later shared Form callback boundary and event-admission changes remain an independently frozen backend candidate until their affected regressions are complete. They are not silently included in this frontend commit. Final aggregate-branch integration must preserve its newer shared UI changes and rebuild combined assets rather than overwrite them with this branch's older base.

## Behavior and intentional limits

- A clone has its own instance identity and encrypted configuration namespace; configuration is copied, history and arbitrary business data are not.
- Creation is cold/paused. It does not invoke plugin business initialization or copy an active schedule.
- Presentation name, description, icon, and display version do not replace the compiled implementation version.
- Existing and retained/tombstoned identifiers conflict rather than overwrite data.
- The framework, not each plugin, owns instance selection and clone creation. Plugins still require their separately verified business implementation and supported runtime capabilities.
