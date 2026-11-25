# SeedRescue 1.6.3 frontend attribution

The eight reachable federation assets retain the installed frontend of
SeedRescue 1.6.3 by landuo. The upstream repository's GNU GPL v3 text is
included in `LICENSE.upstream`. The authored Vue components and build inputs
are available at the pinned upstream commit. The fixed asset inventory,
hashes, and the installed entry's whitespace difference are recorded in
`FRONTEND_ORIGIN.json`.
[Upstream source and build inputs](https://github.com/landuo/MoviePilot-Plugins/tree/3795f1045958fc3e81ecefdf181ad49d8d9e6d7d/plugins.v2/seedrescue)
[Upstream license](https://github.com/landuo/MoviePilot-Plugins/blob/3795f1045958fc3e81ecefdf181ad49d8d9e6d7d/LICENSE)

The emitted remote entry and shared-import helper retain OriginJS
vite-plugin-federation code. The bundled version comparison matches the
OriginJS 1.4.1 implementation in the fixed source checkout's dependency
cache after removing one unused object property. OriginJS attributes its
regex constants to the Ruby
`semantic_range` 3.0.0 gem and one copied regex to node-semver, whose
version it does not identify. The package retains the Mulan PSL v2, MIT,
and ISC notices already preserved for these components in this repository.
[OriginJS license](https://github.com/originjs/vite-plugin-federation/blob/v1.4.1/LICENSE)
[OriginJS semver constants](https://github.com/originjs/vite-plugin-federation/blob/v1.4.1/packages/lib/src/utils/semver/constants.ts)
[semantic_range 3.0.0](https://rubygems.org/gems/semantic_range/versions/3.0.0)
[node-semver license reference](https://github.com/npm/node-semver/blob/v7.3.8/LICENSE)

Vite and its Vue compiler plugin generate the bundles and export helper.
Their MIT notices are included. The host supplies Vue, the registered
Vuetify components, and their styles. The packaged graph contains no local
Vue or Vuetify implementation.
[Upstream Vite configuration](https://github.com/landuo/MoviePilot-Plugins/blob/3795f1045958fc3e81ecefdf181ad49d8d9e6d7d/plugins.v2/seedrescue/vite.config.js)
