import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as realApi from '../src/api.ts';
import * as subscriptions from '../src/features/subscriptions.ts';
import * as exploreMedia from '../src/features/explore-media.ts';
import * as subscriptionFilters from '../src/features/subscription-filters.ts';
import * as calendar from '../src/features/subscription-calendar.ts';
import * as directories from '../src/features/download-directories.ts';

const copy = (value) => JSON.parse(JSON.stringify(value));
const { descriptor } = parse(
  readFileSync(new URL('../src/components/Subscriptions.vue', import.meta.url), 'utf8'),
);
const source = ts.transpileModule(
  compileScript(descriptor, { id: 'Subscriptions', inlineTemplate: true }).content,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;
const media = {
  id: 42,
  title: '合成剧集',
  media_type: 'tv',
  year: 2026,
  seasons: [
    { season_number: 1, episode_count: 20 },
    { season_number: 2, episode_count: 24 },
  ],
};
function subscription() {
  return {
    id: 'synthetic-subscription',
    media: copy(media),
    season: 1,
    target_scope: 'season',
    start_episode: 3,
    total_episodes: 20,
    episodes: [],
    site_ids: ['one'],
    keyword: '原搜索关键词',
    custom_words: ' 原始词 => 原始目标 \n\n',
    state: 'active',
    downloader_id: 'qb',
    download_path: '/data/downloads',
    excluded: [4],
    allow_early: [5],
    episode_dates: { 5: '2026-10-16' },
    policy: { ...realApi.emptyPolicy(), preferred_groups: ['SyntheticGroup'] },
    schedule: { ...realApi.emptySchedule(), exceptions: [{ date: '2026-10-23', action: 'skip' }] },
    mappings: [
      {
        id: 'synthetic-mapping',
        revision: 1,
        media_id: 'tv:42',
        target_season: 1,
        from: 101,
        to: 120,
        offset: -100,
        source: { site: 'one', group: 'SyntheticGroup', form: 'absolute_episode' },
        confirmed: true,
      },
    ],
  };
}
function text(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(text).join(' ');
  if (node.type === vue.Comment) return '';
  return text(
    typeof node.children?.default === 'function' ? node.children.default() : node.children,
  );
}

async function mount(t, { view = 'subscriptions', items = [subscription()], editorOnly = false } = {}) {
  const fixture = {
      items,
      before: copy(items),
      requests: [],
      toasts: [],
      confirmations: [],
      events: [],
      navigation: [],
      confirm: false,
      failSave: false,
    },
    mounted = [],
    module = { exports: {} };
  const settings = {
    sites: [
      { id: 'one', name: '示例一', enabled: true },
      { id: 'two', name: '示例二', enabled: true },
    ],
    downloaders: [
      { id: 'qb', name: 'qBittorrent', enabled: true },
      { id: 'tr', name: 'Transmission', enabled: true },
    ],
    directories: [{ id: 'downloads', source_path: '/data/downloads' }],
  };
  const api = async (path, body, method) => {
    fixture.requests.push({ path, body: body === undefined ? undefined : copy(body), method });
    if (path === '/subscriptions' && body === undefined) return fixture.items;
    if (path === '/catalog/detail?kind=tv&id=42') return copy(media);
    if (path.startsWith('/subscriptions') && body) {
      if (fixture.failSave) throw new Error('订阅保存失败，请稍后重试');
      return { ...copy(body), id: body.id || 'created-subscription' };
    }
    if (path.startsWith('/subscriptions/') && method === 'DELETE') {
      fixture.items = fixture.items.filter((item) => path !== '/subscriptions/' + item.id);
      return { cancelled: true };
    }
    throw new Error('Unexpected request ' + path);
  };
  new Function('require', 'exports', 'confirm', source)(
    (dependency) => {
      if (dependency === 'vue')
        return {
          ...vue,
          onMounted: (callback) => mounted.push(callback),
          withDirectives: (node) => node,
          useId: () => 'test-subscription',
        };
      if (dependency === '../api')
        return {
          ...realApi,
          api,
          ui: { settings, busy: 0 },
          image: () => '',
          go: (target) => fixture.navigation.push(target),
          action: async (callback, success) => {
            try {
              const result = await callback();
              if (success) fixture.toasts.push({ message: success, error: false });
              return result;
            } catch (error) {
              fixture.toasts.push({ message: error.message, error: true });
            }
          },
        };
      if (dependency === '../features/subscriptions') return subscriptions;
      if (dependency === '../features/explore-media') return exploreMedia;
      if (dependency === '../features/subscription-filters') return subscriptionFilters;
      if (dependency === '../features/subscription-calendar') return calendar;
      if (dependency === '../features/download-directories') return directories;
      if (/^vuetify\/components\/(VMenu|VTextarea|VTextField|VSelect)$/.test(dependency)) {
        const name = dependency.split('/').at(-1);
        return { [name]: { name } };
      }
      if (dependency.endsWith('.vue'))
        return { name: dependency.split('/').at(-1).replace('.vue', '') };
      throw new Error('Unexpected dependency ' + dependency);
    },
    module.exports,
    (message) => {
      fixture.confirmations.push(message);
      return fixture.confirm;
    },
  );
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(
      { view, editorOnly },
      { expose() {}, emit: (...event) => fixture.events.push(event) },
    ),
  );
  t.after(() => scope.stop());
  for (const callback of mounted) await callback();
  await vue.nextTick();
  function nodes() {
    const result = [];
    function visit(node) {
      if (node == null) return;
      if (Array.isArray(node)) return node.forEach(visit);
      result.push(node);
      if (typeof node === 'object')
        visit(
          typeof node.children?.default === 'function' ? node.children.default() : node.children,
        );
    }
    visit(render({}, []));
    return result;
  }
  const label = (name) => nodes().find((node) => node.props?.['aria-label'] === name);
  const button = (name) =>
    nodes().find((node) => node.type === 'button' && text(node).trim() === name);
  return {
    fixture,
    nodes,
    label,
    button,
    selectedSites: () =>
      nodes()
        .filter((node) => node.props?.class === 'site-chip')
        .map((node) => text(node).trim()),
    component: (name) => nodes().find((node) => node.type?.name === name),
    dialog: () => nodes().find((node) => node.props?.role === 'dialog'),
    async click(name) {
      const node = label(name) || button(name);
      assert.ok(node, name);
      await node.props.onClick({ preventDefault() {}, stopPropagation() {} });
      await vue.nextTick();
    },
    async update(name, value) {
      const field = { 质量: 'quality', 分辨率: 'resolution', 特效: 'effect' }[name];
      if (field) {
        const node = nodes().find((node) => node.type?.name === 'SubscriptionFilters');
        assert.ok(node, name);
        node.props['onUpdate:modelValue'](
          subscriptionFilters.setSubscriptionFilter(node.props.modelValue, field, value),
        );
        await vue.nextTick();
        return;
      }
      const node = label(name);
      assert.ok(node, name);
      if (node.props['onUpdate:modelValue']) node.props['onUpdate:modelValue'](value);
      else await (node.props.onChange || node.props.onInput)({ target: { value } });
      await vue.nextTick();
    },
    async selectSite(name) {
      const option = nodes().find(
        (node) => node.props?.role === 'option' && text(node).trim() === name,
      );
      assert.ok(option, name);
      await option.props.onClick();
      await vue.nextTick();
    },
  };
}

test('editing across tabs preserves subscription details and saves an isolated draft', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  assert.ok(app.dialog());
  assert.equal(app.button('基础').props.role, 'tab');
  assert.equal(app.button('基础').props['aria-selected'], true);
  await app.update('搜索关键词', '新关键词');
  await app.update('开始集数', 5);
  await app.update('质量', 'WEB-?DL|WEB-?RIP');
  await app.update('分辨率', '1080[pi]|x1080');
  await app.update('特效', 'HDR10|HDR10\\+');
  await app.update('下载器', 'tr');
  await app.update('保存目录', '/data/custom/downloads');
  await app.click('订阅站点选择');
  await app.selectSite('示例二');
  await app.click('进阶');
  assert.equal(app.button('进阶').props['aria-selected'], true);
  await app.update('季', 2);
  await app.update('包含', 'Synthetic');
  await app.update('排除', 'CAM');
  const words = '\n 局部词 => 替换词 \n\nE <> . >> EP+1\n';
  await app.update('自定义识别词', words);
  const schedule = app.component('Schedule').props.model;
  const mappings = app.component('MappingRules').props.model;
  assert.deepEqual(schedule.exceptions, app.fixture.before[0].schedule.exceptions);
  assert.deepEqual(mappings, app.fixture.before[0].mappings);
  schedule.source = 'custom';
  schedule.active_minutes = 45;
  mappings[0].target_season = 2;
  mappings[0].offset = -76;
  await app.click('基础');
  assert.deepEqual(app.selectedSites(), ['示例一', '示例二']);
  await app.click('保存');

  const saved = app.fixture.requests.find((request) => request.method === 'PUT');
  assert.ok(saved);
  assert.equal(saved.path, '/subscriptions/synthetic-subscription');
  assert.deepEqual(
    {
      keyword: saved.body.keyword,
      season: saved.body.season,
      start_episode: saved.body.start_episode,
      total_episodes: saved.body.total_episodes,
      target_scope: saved.body.target_scope,
      episodes: saved.body.episodes,
      downloader_id: saved.body.downloader_id,
      download_path: saved.body.download_path,
      site_ids: saved.body.site_ids,
    },
    {
      keyword: '新关键词',
      season: 2,
      start_episode: 5,
      total_episodes: 24,
      target_scope: 'season',
      episodes: [],
      downloader_id: 'tr',
      download_path: '/data/custom/downloads',
      site_ids: ['one', 'two'],
    },
  );
  assert.deepEqual(saved.body.policy.filter_params, {
    quality: 'WEB-?DL|WEB-?RIP',
    resolution: '1080[pi]|x1080',
    effect: 'HDR10|HDR10\\+',
    include: 'Synthetic',
    exclude: 'CAM',
  });
  assert.equal(saved.body.policy.source, '');
  assert.equal(saved.body.policy.resolution, '');
  assert.equal(saved.body.policy.hdr, '');
  assert.equal(saved.body.policy.include, '');
  assert.equal(saved.body.custom_words, words);
  assert.equal(saved.body.policy.exclude, '');
  assert.deepEqual(saved.body.policy.preferred_groups, ['SyntheticGroup']);
  assert.equal(saved.body.schedule.source, 'custom');
  assert.equal(saved.body.schedule.active_minutes, 45);
  assert.deepEqual(saved.body.schedule.exceptions, app.fixture.before[0].schedule.exceptions);
  assert.equal(saved.body.mappings[0].target_season, 2);
  assert.equal(saved.body.mappings[0].offset, -76);
  assert.deepEqual(saved.body.excluded, [4]);
  assert.deepEqual(saved.body.allow_early, [5]);
  assert.deepEqual(saved.body.episode_dates, { 5: '2026-10-16' });
  assert.deepEqual(
    app.fixture.items,
    app.fixture.before,
    'editing must not mutate the original subscription',
  );
  assert.equal(app.dialog(), undefined);
});

test('clearing subscription recognition words sends an explicit empty string', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  await app.click('进阶');
  assert.equal(app.label('自定义识别词').props.modelValue, app.fixture.before[0].custom_words);
  await app.update('自定义识别词', '');
  await app.click('保存');
  const saved = app.fixture.requests.find((request) => request.method === 'PUT');
  assert.ok(saved);
  assert.equal(saved.body.custom_words, '');
});

test('closing discards edits while canceling a subscription requires confirmation before DELETE', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  await app.update('搜索关键词', '未保存关键词');
  await app.click('关闭');
  assert.equal(app.dialog(), undefined);
  assert.equal(app.fixture.requests.filter((request) => request.method).length, 0);
  await app.click('编辑订阅 合成剧集');
  await app.click('取消订阅');
  assert.equal(app.fixture.confirmations.length, 1);
  assert.equal(app.fixture.requests.filter((request) => request.method === 'DELETE').length, 0);
  assert.ok(app.label('编辑订阅 合成剧集'));
  assert.ok(app.dialog());
  app.fixture.confirm = true;
  await app.click('取消订阅');
  assert.deepEqual(
    app.fixture.requests.filter((request) => request.method === 'DELETE'),
    [{ path: '/subscriptions/synthetic-subscription', body: undefined, method: 'DELETE' }],
  );
  assert.equal(app.label('编辑订阅 合成剧集'), undefined);
  assert.equal(app.dialog(), undefined);
});

test('a new TV subscription retains its starting episode for whole-season and multi-season creation', async (t) => {
  const app = await mount(t, {
    view: 'subscriptions/new/tv/42?season=2&start_episode=5',
    items: [],
  });
  assert.ok(app.dialog());
  await app.click('选择多个季度订阅');
  const multi = app.component('MultiSeasonSubscribe');
  assert.ok(multi);
  assert.equal(multi.props.template.season, 2);
  assert.equal(multi.props.template.start_episode, 5);
  assert.equal(multi.props.template.target_scope, 'season');
  await multi.props.onClose(false);
  await vue.nextTick();
  await app.click('保存');
  const saved = app.fixture.requests.find((request) => request.method === 'POST');
  assert.ok(saved);
  assert.equal(saved.path, '/subscriptions');
  assert.equal(saved.body.season, 2);
  assert.equal(saved.body.start_episode, 5);
  assert.equal(saved.body.target_scope, 'season');
  assert.deepEqual(saved.body.episodes, []);
});

test('the overlay editor saves its selected season and closes without changing the page', async (t) => {
  const app = await mount(t, {
    view: 'subscriptions/new/tv/42?season=2&start_episode=5',
    editorOnly: true,
  });
  assert.ok(app.dialog());
  assert.equal(app.label('新建订阅'), undefined);
  assert.equal(app.label('编辑订阅 合成剧集'), undefined);
  await app.click('保存');
  const saved = app.fixture.requests.find((request) => request.method === 'POST');
  assert.equal(saved.body.season, 2);
  assert.equal(saved.body.start_episode, 5);
  assert.equal(app.fixture.events[0][0], 'saved');
  assert.deepEqual(app.fixture.events[1], ['close']);
  assert.deepEqual(app.fixture.navigation, []);
  assert.equal(app.dialog(), undefined);
});

test('save failures keep the editing dialog and draft without deleting the subscription', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  await app.update('搜索关键词', '失败后保留');
  app.fixture.failSave = true;
  await app.click('保存');
  assert.ok(app.dialog());
  assert.deepEqual(app.fixture.toasts.at(-1), { message: '订阅保存失败，请稍后重试', error: true });
  assert.equal(app.fixture.requests.filter((request) => request.method === 'PUT').length, 1);
  assert.equal(app.fixture.requests.filter((request) => request.method === 'DELETE').length, 0);
  assert.deepEqual(app.fixture.items, app.fixture.before);
  app.fixture.failSave = false;
  await app.click('保存');
  assert.equal(
    app.fixture.requests.filter((request) => request.method === 'PUT').at(-1).body.keyword,
    '失败后保留',
  );
});

test('site keyboard selection starts ArrowUp at the last option and clears to the default site scope', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  assert.deepEqual(app.selectedSites(), ['示例一']);
  let prevented = 0,
    stopped = 0;
  async function key(key) {
    await app.label('搜索订阅站点').props.onKeydown({
      key,
      preventDefault() {
        prevented++;
      },
      stopPropagation() {
        stopped++;
      },
    });
    await vue.nextTick();
  }
  await key('ArrowUp');
  const options = app.nodes().filter((node) => node.props?.role === 'option');
  assert.equal(options.length, 2);
  assert.equal(app.label('搜索订阅站点').props['aria-activedescendant'], options.at(-1).props.id);
  await key('Enter');
  assert.deepEqual(app.selectedSites(), ['示例一', '示例二']);
  assert.equal(prevented, 2);
  await key('Escape');
  assert.equal(stopped, 1, 'Escape is contained by the site dropdown');
  assert.equal(app.nodes().filter((node) => node.props?.role === 'option').length, 0);
  assert.equal(app.label('搜索订阅站点').props['aria-expanded'], false);
  assert.ok(app.dialog(), 'closing the dropdown keeps the subscription editor open');
  await app.click('清除站点');
  assert.deepEqual(app.selectedSites(), []);
  await app.click('保存');
  const saved = app.fixture.requests.find((request) => request.method === 'PUT');
  assert.deepEqual(
    saved.body.site_ids,
    [],
    'an empty selection uses the configured default site scope',
  );
  assert.deepEqual(app.fixture.items, app.fixture.before);
});

test('site search clears after keyboard selection and outside focus without clearing selected sites', async (t) => {
  const app = await mount(t);
  await app.click('编辑订阅 合成剧集');
  const options = () => app.nodes().filter((node) => node.props?.role === 'option');
  async function search(value) {
    await app.update('搜索订阅站点', value);
    await app.label('搜索订阅站点').props.onInput({ target: { value } });
    await vue.nextTick();
  }
  await search('二');
  assert.deepEqual(
    options().map((node) => text(node).trim()),
    ['示例二'],
  );
  for (const key of ['ArrowDown', 'Enter']) {
    await app.label('搜索订阅站点').props.onKeydown({
      key,
      preventDefault() {},
      stopPropagation() {},
    });
    await vue.nextTick();
  }
  assert.deepEqual(app.selectedSites(), ['示例一', '示例二']);
  assert.deepEqual(
    options().map((node) => text(node).trim()),
    ['示例一', '示例二'],
    'choosing a filtered site clears the query and restores the complete option list',
  );
  await search('一');
  assert.deepEqual(
    options().map((node) => text(node).trim()),
    ['示例一'],
  );
  const field = app.nodes().find((node) => node.props?.class === 'subscription-sites');
  await field.props.onFocusout({ currentTarget: { contains: () => false }, relatedTarget: {} });
  await vue.nextTick();
  assert.equal(options().length, 0);
  assert.equal(app.label('搜索订阅站点').props['aria-expanded'], false);
  assert.deepEqual(app.selectedSites(), ['示例一', '示例二']);
  assert.ok(app.dialog());
  await app.click('订阅站点选择');
  assert.deepEqual(
    options().map((node) => text(node).trim()),
    ['示例一', '示例二'],
    'outside focus clears the query before the dropdown is reopened',
  );
  assert.equal(app.fixture.requests.filter((request) => request.method).length, 0);
  assert.deepEqual(app.fixture.items, app.fixture.before);
});
