import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as catalog from '../src/features/plugin-catalog-sources.ts';
import { pluginHostAPI } from '../src/features/plugin-remote-api.ts';
import { exploreMediaKey } from '../src/features/explore-feed.ts';
import { recommendBrowseRoute, recommendBrowseSource } from '../src/features/recommend.ts';
import { recommendEnabled } from '../src/features/recommend.ts';
import {
  exploreImage,
  exploreDetailRoute,
  parseExploreDetailRoute,
} from '../src/features/explore-media.ts';
import { exploreResolvePath, exploreSubscriptionBody } from '../src/features/explore-actions.ts';
const oracle = JSON.parse(
  readFileSync(
    new URL('../../testdata/standard-catalog-extra-ui/source-oracle.json', import.meta.url),
  ),
);
const copy = (value) => JSON.parse(JSON.stringify(value));
function mountFilters(source) {
  const { descriptor } = parse(
    readFileSync(new URL('../src/components/ExtraSourceView.vue', import.meta.url), 'utf8'),
  );
  const compiled = compileScript(descriptor, { id: 'fixed-extra-source' });
  const code = ts.transpileModule(compiled.content, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText;
  const module = { exports: {} };
  const require = (name) => {
    if (name === 'vue') return vue;
    if (name.endsWith('/plugin-catalog-sources')) return catalog;
    if (name.endsWith('/explore-feed'))
      return {
        createExploreFeed: () => ({ generation: 0, results: [] }),
        resetExploreFeed: (feed) => feed.generation++,
      };
    if (name.endsWith('/api')) return { errorText: String };
    if (name.endsWith('.vue') || name.startsWith('vuetify/')) return {};
    throw Error('unexpected SFC import ' + name);
  };
  new Function('require', 'exports', code)(require, module.exports);
  const scope = vue.effectScope();
  const state = scope.run(() =>
    module.exports.default.setup({ source, active: false }, { expose() {} }),
  );
  return { state, scope };
}
test('fixed recommend paths and discover prefixes deduplicate independently of names', () => {
  const r = oracle.recommend;
  const rows = catalog.appendExtraRecommendations(
    r.base.map((row) => ({
      id: row.apipath.slice('recommend/'.length),
      title: row.title,
      category: 'movie',
    })),
    r.input,
  );
  const categories = { movie: '电影', tv: '电视剧', anime: '动漫', rankings: '榜单' };
  assert.deepEqual(
    rows.map((row) => ({
      apipath: row.api_path || 'recommend/' + row.id,
      title: row.title,
      type: categories[row.category] || row.category,
    })),
    r.expected.map(({ apipath, title, type }) => ({ apipath, title, type })),
  );
  const d = oracle.discover;
  assert.deepEqual(
    catalog.appendExtraDiscoverSources(
      d.base.map((row) => row.mediaid_prefix),
      d.input,
    ),
    d.expected.slice(d.base.length),
  );
  assert.equal(recommendBrowseSource(recommendBrowseRoute(rows[1])).api_path, rows[1].api_path);
  assert.equal(
    recommendEnabled(rows)[rows[1].id],
    false,
    'fixed enableConfig is initialized before extras are appended',
  );
  assert.equal(recommendEnabled(rows, { [rows[1].id]: true })[rows[1].id], true);
  const initial = recommendEnabled(rows);
  assert.deepEqual(
    rows.filter((row) => initial[row.id]).map((row) => row.api_path || 'recommend/' + row.id),
    r.visible_after_append.map((row) => row.apipath),
  );
  const sameTitle = {
    id: 'plugin/Owned/same-name',
    api_path: 'plugin/Owned/same-name',
    title: rows[0].title,
    category: 'movie',
  };
  assert.equal(recommendEnabled([...rows, sameTitle])[sameTitle.id], true);
  assert.equal(
    recommendEnabled([...rows, sameTitle], { [rows[0].id]: false })[sameTitle.id],
    false,
  );
});
test('opaque source routes, resolution and public images retain source identity', () => {
  const item = {
    source: 'owned',
    source_id: 'opaque/甲 ?#%',
    id: 0,
    media_type: 'tv',
    poster_path: 'https://owned.invalid/poster.png',
  };
  assert.deepEqual(parseExploreDetailRoute(exploreDetailRoute(item)), {
    source: item.source,
    sourceId: item.source_id,
    kind: 'tv',
  });
  const query = new URL(exploreResolvePath(item), 'https://owned.invalid').searchParams;
  assert.equal(query.get('source'), 'owned');
  assert.equal(query.get('source_id'), item.source_id);
  assert.throws(() => exploreSubscriptionBody(item));
  assert.equal(exploreImage(item), item.poster_path);
  assert.equal(exploreImage(item, 'https://user:password@owned.invalid/poster.png'), '');
  assert.equal(exploreImage(item, 'javascript:alert(1)'), '');
});
test('fixed media detail title/year hints survive opaque route encoding and resolve requests', () => {
  // Fixed MediaCard.vue forwards original title/year with source and mediaid.
  // They are identification hints, never a replacement TMDB identifier.
  const item = {
    source: 'owned',
    source_id: 'opaque/甲 ?#%',
    id: 0,
    media_type: 'tv',
    title: '原标题 & ?#甲',
    year: 2026,
  };
  assert.deepEqual(parseExploreDetailRoute(exploreDetailRoute(item)), {
    source: 'owned',
    kind: 'tv',
    sourceId: item.source_id,
    title: item.title,
    year: '2026',
  });
  const query = new URL(exploreResolvePath(item), 'https://owned.invalid').searchParams;
  assert.equal(query.get('source_id'), item.source_id);
  assert.equal(query.get('title'), item.title);
  assert.equal(query.get('year'), '2026');
  assert.throws(() => exploreSubscriptionBody(item));
});
for (const c of oracle.filters)
  test('actual ExtraSourceView matches fixed watcher: ' + c.name, async () => {
    const { state, scope } = mountFilters({
      filter_params: copy(c.initial),
      filter_ui: [],
      depends: c.depends,
      api_path: 'plugin/Owned/feed',
    });
    try {
      Object.assign(state.filters, c.change);
      let error;
      try {
        await vue.nextTick();
      } catch (e) {
        error = String(e);
      }
      assert.equal(error, c.error);
      assert.deepEqual(copy(state.filters), c.expected);
      assert.equal(state.feed.generation, c.reset_count);
    } finally {
      scope.stop();
    }
  });
test('actual Axios preserves fixed query/page order and opaque MediaInfo identity', async (t) => {
  const previous = pluginHostAPI.defaults.adapter;
  t.after(() => (pluginHostAPI.defaults.adapter = previous));
  const row = {
    source: 'provider',
    mediaid_prefix: 'owned',
    media_id: 'A/B ?#',
    type: '电视剧',
    title: 'Owned opaque',
    year: '2026',
    tmdb_id: 4242,
    imdb_id: 'tt42',
    season: 2,
    vote_average: '8.2',
    custom: { preserved: true },
  };
  const requests = [];
  pluginHostAPI.defaults.adapter = async (config) => {
    requests.push({ url: pluginHostAPI.getUri(config), params: config.params });
    return { data: [row], status: 200, statusText: 'OK', headers: {}, config };
  };
  for (const c of oracle.pagination) {
    const result = await catalog.requestPluginCatalogPage(
      'plugin/Owned/feed?list=ordinary',
      2,
      c.input || {},
    );
    assert.deepEqual(requests.at(-1).params, c.expected);
    assert.match(requests.at(-1).url, /\/api\/v1\/plugin\/Owned\/feed\?list=ordinary/);
    assert.equal(result.results[0].id, 0);
    assert.equal(result.results[0].source_id, row.media_id);
    assert.equal(result.results[0].media_type, 'tv');
    assert.equal(result.results[0].vote_average, 8.2);
    assert.equal(result.results[0].original_media, row);
  }
  assert.notEqual(
    exploreMediaKey(catalog.pluginCatalogMedia(row)),
    exploreMediaKey(catalog.pluginCatalogMedia({ ...row, imdb_id: 'tt43' })),
  );
  pluginHostAPI.defaults.adapter = async (config) => ({
    data: [],
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  });
  assert.equal((await catalog.requestPluginCatalogPage('plugin/Owned/feed', 1)).has_more, false);
  pluginHostAPI.defaults.adapter = async (config) => {
    assert.equal(config.url.includes('list='), false);
    assert.deepEqual(config.params, {
      page: '7',
      list: 'ordinary',
      title: ['Old title', 'Owned title'],
    });
    return { data: [], status: 200, statusText: 'OK', headers: {}, config };
  };
  await catalog.requestRecommendBrowsePage(
    {
      id: 'owned',
      title: 'Owned title',
      category: 'movie',
      api_path: 'plugin/Owned/feed?list=ordinary&page=7&title=Old+title',
    },
    2,
    'Owned title',
  );
});
