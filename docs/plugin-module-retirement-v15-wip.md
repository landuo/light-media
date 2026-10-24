# Legacy plugin retirement: core boundary checkpoint

This partial checkpoint builds on 52496456cf8fc771476b927838652a1319ffdbd8. It continues removing the old framework without claiming full replacement-module acceptance, completed business-plugin adaptation or main integration.

## Scope

- Retire twelve whole files: ten old production adapter/HTTP/dashboard/fast-lane files and two paired legacy tests. Exact Git identities and paths are listed in testdata/plugin-retirement-v15-evidence.json.
- In two mixed lifecycle test files, remove only branches calling retired fast-lane adapters and their unused imports. Remaining tests are retained; removed old-adapter tests are not reported as passing.
- Remove old plugin-specific Lark/FNOS gate dependencies from core jobs and asset scraping. An unadapted Lark job explicitly reports PLUGIN_ADAPTER_UNAVAILABLE rather than invoking the retired adapter. Core FNOS scan/visibility behavior is checked against an owned loopback service, not replaced with a success stub.
- Add two real core-boundary regressions. The existing disabled-FNOS negative case and all nine direct-manager module roots from the previous checkpoint remain in the selected gate.

## Actual validation

The exact frozen input contains 2,323 effective Go files with no SHA256 drift. The same twelve selected roots passed in ordinary and application-race modes, with exit zero, no skipped/failed roots, empty stderr and no recorded I/O errors. Package times were 5.102 and 25.290 seconds.

The three core-boundary roots are TestOriginalCoreFNOSScanAndVisibilityWithoutNativeGate, TestOriginalRemovedLarkCoreJobExplicitlyUnavailable and TestFNOSDisabledNeverClaimsVisible. The remaining nine cover the previously recorded direct standard lifecycle, real version upgrade, failure boundaries and public-service protections.

The owned fixtures were rebuilt from the same overlay. Worker binaries were ordinary builds; race instrumentation applied to the App tests. The commands used -vet=off. This is selected regression evidence, not a full-project or browser acceptance result.

## Remaining work

Other legacy framework source/data-tool dependencies remain. Subsequent extraction drafts are not included here. Full ordinary/race regression, vet, frontend/build checks, normal-server browser workflows and embedded-asset verification still need completion before final module acceptance. Business-plugin repairs come afterward. Git history, earlier evidence, algorithms outside this scoped change and user data are preserved.
