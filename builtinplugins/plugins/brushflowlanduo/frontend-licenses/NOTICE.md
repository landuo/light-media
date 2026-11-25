# BrushFlowLanduo 5.2.18 frontend attribution

The 14 reachable frontend assets retain the installed bytes of BrushFlowLanduo
5.2.18 by jxxghp and landuo. They are bound to the upstream
`plugins.v2/brushflowlanduo` directory at commit
`3795f1045958fc3e81ecefdf181ad49d8d9e6d7d`. The asset inventory and digests are in
`FRONTEND_ORIGIN.json`. The plugin repository's `LICENSE.upstream` is the exact
GNU GPL v3 text from that commit, SHA-256
`3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986`.
[Upstream plugin source](https://github.com/landuo/MoviePilot-Plugins/tree/3795f1045958fc3e81ecefdf181ad49d8d9e6d7d/plugins.v2/brushflowlanduo)

`index-Cezq_yMy.js` identifies Module Federation 2.9.3 and includes its runtime,
runtime-core, SDK and error-codes modules. The repository-level MIT text covers
the project and error-codes. The runtime and runtime-core package license
files are byte-identical. The SDK has its own MIT notice.
[Runtime-core package](https://github.com/module-federation/core/blob/v2.9.3/packages/runtime-core/package.json)
[Error-codes package](https://github.com/module-federation/core/blob/v2.9.3/packages/error-codes/package.json)
[Runtime-core package license](https://github.com/module-federation/core/blob/v2.9.3/packages/runtime-core/LICENSE)

The bundled runtime-core semver parser retains code forked from
OriginJS vite-plugin-federation v1.1.12 under Mulan PSL v2. The original source
attributes that parser to OriginJS and includes the Mulan notice.
[Runtime-core semver source](https://github.com/module-federation/core/blob/v2.9.3/packages/runtime-core/src/utils/semver/index.ts)
[OriginJS semver source](https://github.com/originjs/vite-plugin-federation/blob/v1.1.12/packages/lib/src/utils/semver/index.ts)

The bundle contains emitted Module Federation Vite container code, the Vite
preload helper and the Vite Vue SFC export helper. The pinned source declares
`@module-federation/vite ^1.23.10`, `vite ^5.4.11` and
`@vitejs/plugin-vue ^5.0.4`. Their version tags below pin the notice sources.
The installed source snapshot has no frontend lockfile for resolving exact
build-tool patch versions.
[Installed package declarations](https://github.com/landuo/MoviePilot-Plugins/blob/3795f1045958fc3e81ecefdf181ad49d8d9e6d7d/plugins.v2/brushflowlanduo/package.json)

The fixed semver constants also acknowledge their basis in Ruby
`semantic_range` 3.0.0 and a regular expression copied from node-semver. Their
MIT and ISC license texts are included for those fragments. The Ruby license
comes from the exact published gem. The fixed upstream code does not identify
the original node-semver version, so its notice uses the pinned upstream
v7.3.8 ISC text as the reference.
[Fixed semver constants](https://github.com/module-federation/core/blob/v2.9.3/packages/runtime-core/src/utils/semver/constants.ts)

The host supplies the external Vue and Vuetify modules and the
`vuetify/styles` share. This plugin archive contains no standalone Vue or
Vuetify library. The host continues to supply its own component styles.

| Package member | SHA-256 | License source |
| --- | --- | --- |
| `Module-Federation-Core-LICENSE` | `8385c252568609183ceb9aead4ebac3b3bd8678c627739aee7aad29116409332` | [Official pinned license](https://raw.githubusercontent.com/module-federation/core/v2.9.3/LICENSE) |
| `Module-Federation-Runtime-LICENSE` | `74aeeff0b0117761910083b03d18a64a5a654917e794a841406410f909b427cf` | [Official pinned license](https://raw.githubusercontent.com/module-federation/core/v2.9.3/packages/runtime/LICENSE) |
| `Module-Federation-SDK-LICENSE` | `62431fbe2f17fc691be95ed8302aa042c03200d2a7cdee7aab4bb0d2d3e16098` | [Official pinned license](https://raw.githubusercontent.com/module-federation/core/v2.9.3/packages/sdk/LICENSE) |
| `Module-Federation-Vite-LICENSE` | `0ec4954b495a967b9bf3f4dc927f3005d5c2c403716adcbe609274834b590a5d` | [Official pinned license](https://raw.githubusercontent.com/module-federation/vite/1.23.10/LICENSE) |
| `OriginJS-Vite-Plugin-Federation-LICENSE` | `d0b16a3cb603569486834cb55fa8a539832063864793339386f5e1f646928987` | [Official pinned license](https://raw.githubusercontent.com/originjs/vite-plugin-federation/v1.1.12/LICENSE) |
| `Vite-LICENSE` | `692057af3d664cbb79ac38293eb50aa3c4987f8182e2a440136e59e08f1b4a54` | [Official pinned license](https://raw.githubusercontent.com/vitejs/vite/v5.4.11/LICENSE) |
| `Vite-Plugin-Vue-LICENSE` | `29b68325fe026047d13e187b44c33b2acacf7dc647dec4583702e59f235e13b5` | [Official pinned license](https://raw.githubusercontent.com/vitejs/vite-plugin-vue/plugin-vue@5.0.4/LICENSE) |
| `Semantic-Range-LICENSE` | `ba63a93ee680c21f746ec1f95b22b92aa781743137d718ef0db08335125dadc3` | [Official pinned source](https://rubygems.org/downloads/semantic_range-3.0.0.gem) |
| `Node-Semver-LICENSE` | `4ec3d4c66cd87f5c8d8ad911b10f99bf27cb00cdfcff82621956e379186b016b` | [Official pinned source](https://raw.githubusercontent.com/npm/node-semver/v7.3.8/LICENSE) |
