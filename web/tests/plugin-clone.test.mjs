import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import { APIError, errorText } from '../src/api.ts';
import * as cloneHelpers from '../src/features/plugin-clone.ts';
import * as organization from '../src/features/plugin-organization.ts';
import * as marketHelpers from '../src/features/plugin-market.ts';
import * as projectHelpers from '../src/features/plugin-project.ts';
import * as forms from '../src/features/plugin-forms.ts';

const { emptyPluginClone, pluginCloneErrors, pluginCloneInstanceId, safePluginIcon } = cloneHelpers;
const originalCloneDefaults = JSON.parse(readFileSync(new URL('../../testdata/plugin-clone-ui-defaults-20261106/source-oracle.json', import.meta.url))).cases[0].initial_model;
const plugin = (overrides = {}) => ({
  plugin_id: 'SeedRescue',
  instance_id: 'SeedRescue2',
  name: '辅种分身',
  description: '来源描述',
  version: '1.2.3',
  display_version: 'view-9',
  icon: '/plugin_icon/seed.png',
  author: '合成测试',
  author_url: 'https://synthetic.invalid',
  installed: true,
  state: false,
  ...overrides,
});
const validForm = (overrides = {}) => ({ ...emptyPluginClone(), suffix: 'CopyA', ...overrides });
const resultFor = (source, form) => ({
  created: true,
  instance_id: pluginCloneInstanceId(source, form.suffix),
  source_instance_id: source.instance_id,
  plugin_id: source.plugin_id,
});

test('clone identity uses the complete instance ID and lowercases only the suffix', () => {
  assert.equal(pluginCloneInstanceId(plugin(), 'CopyA'), 'SeedRescue2copya');
  assert.deepEqual(pluginCloneErrors(plugin(), validForm()), {});
  for (const suffix of ['', 'a-b', '_x', '复制', 'a b', 'a\n', 'a'.repeat(21)])
    assert.match(pluginCloneErrors(plugin(), validForm({ suffix })).suffix, /1 至 20/);
  assert.deepEqual(
    pluginCloneErrors(plugin({ instance_id: 'x'.repeat(95) }), validForm({ suffix: 'A' })),
    {},
  );
  assert.match(
    pluginCloneErrors(plugin({ instance_id: 'x'.repeat(255) }), validForm({ suffix: 'A' })).suffix,
    /255/,
  );
  assert.match(pluginCloneErrors(plugin({ instance_id: '' }), validForm()).suffix, /来源实例/);
  assert.match(
    pluginCloneErrors(plugin({ instance_id: '../bad' }), validForm()).suffix,
    /来源实例/,
  );
});

test('optional values remain empty for server inheritance and metadata uses UTF-8 byte limits', () => {
  assert.deepEqual(validForm(), {
    suffix: 'CopyA',
    name: '',
    description: '',
    version: '',
    icon: '',
  });
  assert.deepEqual(pluginCloneErrors(plugin(), validForm({ name: ' ' })), {});
  for (const [field, limit] of [
    ['name', 256],
    ['description', 4096],
    ['version', 64],
    ['icon', 2048],
  ]) {
    assert.equal(
      pluginCloneErrors(plugin(), validForm({ [field]: 'a'.repeat(limit) }))[field],
      undefined,
    );
    assert.match(
      pluginCloneErrors(plugin(), validForm({ [field]: 'a'.repeat(limit + 1) }))[field],
      /UTF-8/,
    );
    assert.match(
      pluginCloneErrors(plugin(), validForm({ [field]: '中'.repeat(Math.floor(limit / 3) + 1) }))[
        field
      ],
      /UTF-8/,
    );
    for (const control of ['\n', '\t', '\u0000', '\u007f', '\u0085'])
      assert.ok(pluginCloneErrors(plugin(), validForm({ [field]: 'x' + control }))[field]);
  }
  assert.equal(
    pluginCloneErrors(plugin(), validForm({ version: '9.1_rc+display-2' })).version,
    undefined,
  );
  for (const version of ['_first', '1 2', '版本', '9/2'])
    assert.match(pluginCloneErrors(plugin(), validForm({ version })).version, /显示版本/);
});

test('icons allow real HTTP/HTTPS and mounted relative assets, never scripts, credentials or protocol-relative URLs', () => {
  for (const value of [
    'javascript:alert(1)',
    'data:image/svg+xml,bad',
    'blob:https://synthetic.invalid/id',
    'file:///tmp/icon.png',
    '//synthetic.invalid/icon.png',
    '\\synthetic.invalid/icon.png',
    '/\\synthetic.invalid/icon.png',
    'https://user:secret@synthetic.invalid/x',
    'https://%75ser@synthetic.invalid/x',
    'https://@synthetic.invalid/',
    'http://:@synthetic.invalid/',
    'https:///missing-host/x',
    'https://',
    'https://synthetic.invalid/\nicon.png',
    '\tjavascript:alert(1)',
    'https://synthetic.invalid/bad%ZZ.png',
  ]) {
    assert.equal(safePluginIcon(value), '', value);
    assert.ok(pluginCloneErrors(plugin(), validForm({ icon: value })).icon, value);
  }
  assert.equal(
    safePluginIcon('https://synthetic.invalid/icon.svg'),
    'https://synthetic.invalid/icon.svg',
  );
  assert.equal(
    safePluginIcon('http://synthetic.invalid/icon.png'),
    'http://synthetic.invalid/icon.png',
  );
  assert.equal(
    safePluginIcon('https://synthetic.invalid/icon with spaces.png'),
    'https://synthetic.invalid/icon%20with%20spaces.png',
  );
  assert.equal(safePluginIcon('statistic.png', '/'), '/plugin_icon/statistic.png');
  assert.equal(
    safePluginIcon('/plugin_icon/seed.png', '/light-media/'),
    '/light-media/plugin_icon/seed.png',
  );
  assert.equal(
    safePluginIcon('images/custom.svg', '/light-media/'),
    '/light-media/images/custom.svg',
  );
  assert.equal(
    safePluginIcon('/light-media/images/custom.svg', '/light-media/'),
    '/light-media/images/custom.svg',
  );
  assert.equal(safePluginIcon('../escape.png', '/light-media/'), '');
});

// Compile and execute the production SFCs. Only external UI primitives, browser
// lifecycle and transport are stubbed; assertions drive actual render handlers.
const scripts = new Map();
function scriptFor(name) {
  if (!scripts.has(name)) {
    const { descriptor } = parse(
      readFileSync(new URL(`../src/components/${name}.vue`, import.meta.url), 'utf8'),
    );
    scripts.set(
      name,
      ts.transpileModule(compileScript(descriptor, { id: name, inlineTemplate: true }).content, {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
      }).outputText,
    );
  }
  return scripts.get(name);
}
async function mount(t, name, { props = {}, api, emit, desktop = true } = {}) {
  const requests = [],
    events = [],
    toasts = [],
    mounted = [],
    unmounted = [],
    polls = [],
    pending = [];
  const module = { exports: {} };
  const reactiveProps = vue.reactive(props);
  new Function('require', 'exports', 'setInterval', 'clearInterval', scriptFor(name))(
    (dependency) => {
      if (dependency === 'vue')
        return {
          ...vue,
          onMounted: (fn) => mounted.push(fn),
          onUnmounted: (fn) => unmounted.push(fn),
          defineAsyncComponent: (loader) => ({
            name: loader.toString().match(/\.\/(\w+)\.vue/)[1],
          }),
          withDirectives: (node) => node,
        };
      if (dependency === '../api')
        return {
          APIError,
          errorText,
          toast: (text) => toasts.push(text),
          api: async (path, body, method) => {
            requests.push({ path, body: body === undefined ? undefined : { ...body }, method });
            if (api) return api(path, body, method);
            return resultFor(reactiveProps.plugin, body);
          },
        };
      if (dependency === '../features/plugin-clone') return cloneHelpers;
      if (dependency.endsWith('plugin-icon-colors.json'))
        return JSON.parse(
          readFileSync(new URL('../src/features/plugin-icon-colors.json', import.meta.url)),
        );
      if (dependency === '../features/plugin-organization') return organization;
      if (dependency === '../features/plugin-market') return marketHelpers;
      if (dependency === '../features/plugin-project') return projectHelpers;
      if (dependency === '../features/plugin-forms') return forms;
      if (dependency === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(desktop) }) };
      if (dependency.startsWith('vuetify/'))
        return new Proxy({}, { get: (_, key) => ({ name: key }) });
      if (dependency === 'vuedraggable') return { name: 'Draggable' };
      if (dependency.endsWith('.vue'))
        return { name: dependency.split('/').at(-1).replace('.vue', '') };
      throw new Error('Unexpected dependency ' + dependency);
    },
    module.exports,
    (fn) => {
      polls.push(fn);
      return 1;
    },
    () => {},
  );
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(reactiveProps, {
      expose() {},
      emit(event, ...values) {
        events.push([event, ...values]);
        const task = emit?.(event, ...values);
        if (task?.then) pending.push(task);
      },
    }),
  );
  t.after(() => {
    unmounted.forEach((fn) => fn());
    scope.stop();
  });
  const flush = async () => {
    await Promise.all(pending.splice(0));
    for (let i = 0; i < 8; i++) await vue.nextTick();
  };
  for (const fn of mounted) await fn();
  await flush();
  const nodes = (root) => {
    const all = [];
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) return node.forEach(visit);
      all.push(node);
      if (node.type?.name === 'Draggable')
        for (const element of node.props.modelValue) visit(node.children.item({ element }));
      else if (node.children?.default) visit(node.children.default({ props: {} }));
      else visit(node.children);
      if (node.children?.activator) visit(node.children.activator({ props: {} }));
    }
    visit(root ?? render({}, []));
    return all;
  };
  const component = (name) => nodes().find((node) => node.type?.name === name);
  const label = (text) => nodes().find((node) => node.props?.['aria-label'] === text);
  const text = (node) => {
    if (typeof node === 'string') return node;
    if (!node) return '';
    if (Array.isArray(node)) return node.map(text).join(' ');
    return text(node.children?.default ? node.children.default({ props: {} }) : node.children);
  };
  const button = (value) =>
    nodes().find((node) => node.type?.name === 'VBtn' && text(node).trim() === value);
  return {
    props: reactiveProps,
    requests,
    events,
    toasts,
    polls,
    nodes,
    component,
    label,
    text,
    button,
    flush,
    async set(labelText, value) {
      label(labelText).props['onUpdate:modelValue'](value);
      await flush();
    },
    submit() {
      return component('VCard').props.onSubmit({ preventDefault() {} });
    },
  };
}

test('clone dialog prefills fixed original frontend defaults with editable fields and responsive layout', async (t) => {
  const dialog = await mount(t, 'PluginCloneDialog', { props: { plugin: plugin() } });
  assert.equal(dialog.component('VDialog').props['max-width'], '600');
  assert.equal(dialog.component('VDialog').props.fullscreen, false);
  assert.equal(dialog.nodes().filter((node) => node.type?.name === 'VTextField').length, 5);
  for (const [name, placeholder] of [
    ['分身名称', '辅种分身'],
    ['分身描述', '来源描述'],
    ['分身版本', 'view-9'],
  ]) {
    assert.equal(dialog.label(name).props.modelValue, originalCloneDefaults[{ '分身名称':'name', '分身描述':'description', '分身版本':'version' }[name]]);
    assert.equal(dialog.label(name).props.placeholder, placeholder);
  }
  await dialog.set('分身后缀', 'CopyA');
  assert.match(dialog.text(dialog.component('VCard')), /SeedRescue2copya/);
  assert.match(dialog.text(dialog.component('VCard')), /仅复制配置/);
  assert.match(dialog.text(dialog.component('VCard')), /不复制历史记录及运行数据/);
  assert.match(dialog.text(dialog.component('VCard')), /来源配置非空时关闭 enable、enabled/);
  assert.match(dialog.text(dialog.component('VCard')), /来源配置为空时按插件默认值初始化，可能自动启用/);
  const mobile = await mount(t, 'PluginCloneDialog', {
    props: { plugin: plugin() },
    desktop: false,
  });
  assert.equal(mobile.component('VDialog').props.fullscreen, true);
});

// Expected bodies come from executing fixed2829 Card.executePluginClone,
// independently of this SFC and its native API route adaptation.
test('fixed original Card submit normalizes all five clone fields and preserves empty strings', async (t) => {
  const sourceCases = JSON.parse(readFileSync(new URL('../../testdata/plugin-clone-ui-defaults-20261106/source-submit-oracle.json', import.meta.url))).cases;
  for (const sample of sourceCases) await t.test(sample.id, async (t) => {
    const source = sample.id === 'empty-source-version'
      ? plugin({name:'无版本插件',description:'',version:'',display_version:'',icon:''})
      : plugin();
    const dialog = await mount(t, 'PluginCloneDialog', { props: { plugin: source } });
    for (const [key,label] of [['suffix','分身后缀'],['name','分身名称'],['description','分身描述'],['version','分身版本'],['icon','分身图标']])
      await dialog.set(label,sample.form[key]);
    await dialog.submit();await dialog.flush();
    assert.deepEqual(dialog.requests.map(row=>row.body),sample.actual_posts.map(row=>row.body));
  });
});

test('cancel, close and outside/escape dismissal send no request; reopening starts a clean form', async (t) => {
  for (const dismiss of [
    (dialog) => dialog.button('取消').props.onClick(),
    (dialog) => dialog.label('关闭插件分身').props.onClick(),
    (dialog) => dialog.component('VDialog').props['onUpdate:modelValue'](false),
  ]) {
    const dialog = await mount(t, 'PluginCloneDialog', { props: { plugin: plugin() } });
    await dialog.set('分身后缀', 'draft');
    dismiss(dialog);
    assert.deepEqual(dialog.events, [['close']]);
    assert.equal(dialog.requests.length, 0);
  }
  const fresh = await mount(t, 'PluginCloneDialog', { props: { plugin: plugin() } });
  assert.equal(fresh.label('分身后缀').props.modelValue, '');
  assert.equal(fresh.requests.length, 0);
});

test('invalid suffix, total ID length, version and icon never reach transport', async (t) => {
  const dialog = await mount(t, 'PluginCloneDialog', { props: { plugin: plugin() } });
  await dialog.submit();
  assert.match(dialog.label('分身后缀').props['error-messages'], /1 至 20/);
  await dialog.set('分身后缀', '../bad');
  await dialog.submit();
  await dialog.set('分身后缀', 'valid');
  await dialog.set('分身版本', 'invalid version');
  await dialog.set('分身图标', 'javascript:alert(1)');
  await dialog.submit();
  assert.ok(dialog.label('分身版本').props['error-messages']);
  assert.ok(dialog.label('分身图标').props['error-messages']);
  assert.equal(dialog.requests.length, 0);
  const tooLong = await mount(t, 'PluginCloneDialog', {
    props: { plugin: plugin({ instance_id: 'x'.repeat(255) }) },
  });
  await tooLong.set('分身后缀', 'a');
  await tooLong.submit();
  assert.match(tooLong.label('分身后缀').props['error-messages'], /255/);
  assert.equal(tooLong.requests.length, 0);
});

test('one in-flight clone locks fields/dismissal and success cannot resubmit even before unmount', async (t) => {
  let resolve;
  const source = plugin();
  const dialog = await mount(t, 'PluginCloneDialog', {
    props: { plugin: source },
    api: () =>
      new Promise((done) => {
        resolve = done;
      }),
  });
  await dialog.set('分身后缀', 'CopyA');
  await dialog.set('分身名称', '自定义名称');
  await dialog.set('分身描述', '自定义描述');
  await dialog.set('分身版本', 'display-3');
  await dialog.set('分身图标', 'https://synthetic.invalid/custom.svg');
  const first = dialog.submit();
  await dialog.submit();
  assert.equal(dialog.requests.length, 1);
  assert.deepEqual(dialog.requests[0], {
    path: '/plugins/SeedRescue2/clone',
    method: undefined,
    body: validForm({
      name: '自定义名称',
      description: '自定义描述',
      version: 'display-3',
      icon: 'https://synthetic.invalid/custom.svg',
    }),
  });
  assert.equal(dialog.component('VDialog').props.persistent, true);
  assert.equal(dialog.button('创建分身').props.loading, true);
  assert.ok(
    dialog
      .nodes()
      .filter((node) => node.type?.name === 'VTextField')
      .every((node) => node.props.disabled),
  );
  dialog.button('取消').props.onClick();
  dialog.label('关闭插件分身').props.onClick();
  dialog.component('VDialog').props['onUpdate:modelValue'](false);
  assert.deepEqual(dialog.events, []);
  resolve(resultFor(source, validForm()));
  await first;
  await dialog.flush();
  assert.deepEqual(dialog.events, [['created', resultFor(source, validForm())]]);
  assert.equal(source.version, '1.2.3');
  await dialog.submit();
  assert.equal(dialog.requests.length, 1);
});

test('API errors preserve form and request ID; conflicts include retained tombstones and migration is not mislabeled as permissions', async (t) => {
  for (const [code, message, hint] of [
    ['PLUGIN_CLONE_CONFLICT', '分身编号与内置插件编号冲突', /已卸载但仍保留数据/],
    [
      'PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED',
      '来源实例仍绑定专用下载器迁移引用',
      /专用迁移引用尚未转换/,
    ],
    ['PLUGIN_NOT_INSTALLED', '来源插件已卸载', /来源插件已卸载/],
    ['INTERNAL_ERROR', '合成服务错误', /合成服务错误/],
  ]) {
    let fail = true;
    const source = plugin();
    const dialog = await mount(t, 'PluginCloneDialog', {
      props: { plugin: source },
      api: (_path, body) => {
        if (fail) throw new APIError(code, message, 'clone-request-17');
        return resultFor(source, body);
      },
    });
    await dialog.set('分身后缀', 'Keep');
    await dialog.set('分身名称', '保留输入');
    await dialog.submit();
    assert.equal(dialog.events.length, 0);
    assert.equal(dialog.label('分身后缀').props.modelValue, 'Keep');
    assert.equal(dialog.label('分身名称').props.modelValue, '保留输入');
    const alert = dialog.nodes().find((node) => node.props?.role === 'alert');
    assert.match(dialog.text(alert), hint);
    assert.ok(dialog.text(alert).includes(message));
    assert.match(dialog.text(alert), /clone-request-17/);
    assert.doesNotMatch(dialog.text(alert), /权限/);
    assert.equal(dialog.component('VDialog').props.persistent, false);
    fail = false;
    await dialog.set('分身后缀', 'Another');
    await dialog.submit();
    assert.equal(dialog.events[0][0], 'created');
    assert.equal(dialog.requests.length, 2);
  }
});

test('malformed or mismatched success envelopes do not announce creation', async (t) => {
  for (const response of [
    undefined,
    {},
    { created: false },
    { ...resultFor(plugin(), validForm()), instance_id: 'SeedRescuecopya' },
  ]) {
    const dialog = await mount(t, 'PluginCloneDialog', {
      props: { plugin: plugin() },
      api: () => response,
    });
    await dialog.set('分身后缀', 'CopyA');
    await dialog.submit();
    assert.equal(dialog.events.length, 0);
    assert.match(
      dialog.text(dialog.nodes().find((node) => node.props?.role === 'alert')),
      /结果无法确认/,
    );
  }
});

test('card uses display version and safe real icon, resets failure on icon change and emits the exact clone instance', async (t) => {
  const source = plugin({ icon: 'https://synthetic.invalid/custom.svg' });
  const card = await mount(t, 'PluginManageCard', { props: { plugin: source } });
  assert.match(card.text(card.component('VCard')), /vview-9/);
  assert.doesNotMatch(card.text(card.component('VCard')), /v1\.2\.3/);
  const icon = card.nodes().find((node) => node.type === 'img');
  assert.equal(icon.props.src, source.icon);
  assert.equal(icon.props.referrerpolicy, 'no-referrer');
  icon.props.onError();
  await card.flush();
  assert.equal(
    card.nodes().find((node) => node.type === 'img').props.src,
    '/plugin_icon/plugin.png',
  );
  card.props.plugin.icon = '/plugin_icon/seed.png';
  await card.flush();
  assert.equal(card.nodes().find((node) => node.type === 'img').props.src, '/plugin_icon/seed.png');
  const menu = card
    .nodes()
    .find((node) => node.type?.name === 'VListItem' && node.props.title === '分身');
  menu.props.onClick();
  assert.equal(card.events[0][0], 'action');
  assert.equal(card.events[0][1], 'clone');
  assert.equal(card.events[0][2].instance_id, 'SeedRescue2');
  assert.equal(source.version, '1.2.3');
  card.props.busy = true;
  await card.flush();
  card
    .nodes()
    .find((node) => node.type?.name === 'VListItem' && node.props.title === '分身')
    .props.onClick();
  assert.equal(card.events.length, 1);
  card.props.plugin.icon = 'javascript:alert(1)';
  card.props.plugin.display_version = undefined;
  await card.flush();
  assert.equal(
    card.nodes().find((node) => node.type === 'img').props.src,
    '/plugin_icon/plugin.png',
  );
  assert.match(card.text(card.component('VCard')), /v1\.2\.3/);
});

test('market/uninstalled or sortable cards have no clone menu', async (t) => {
  for (const props of [
    { plugin: plugin({ installed: false }) },
    { plugin: plugin(), sortable: true },
  ]) {
    const card = await mount(t, 'PluginManageCard', { props });
    assert.equal(
      card.nodes().find((node) => node.type?.name === 'VListItem' && node.props.title === '分身'),
      undefined,
    );
  }
});

test('actual card-to-dialog flow creates once, refreshes lists and statistics and keeps the source folder without an organization write', async (t) => {
  const source = plugin({ state: true });
  let installed = [source];
  let groups = { revision: 1, folders: { 原文件夹: [source.instance_id] }, order: [] };
  const allRequests = [];
  const api = async (path, body, method) => {
    allRequests.push({ path, body, method });
    if (path === '/plugins?state=installed') return [...installed];
    if (path === '/plugins/organization' && !body) return structuredClone(groups);
    if (path === '/plugins/statistic') return {};
    if (path.startsWith('/plugins/market?')) return { items: [], sources: [] };
    if (path === '/plugins/SeedRescue2/clone') {
      const result = resultFor(source, body);
      installed.push(
        plugin({
          instance_id: result.instance_id,
          source_instance_id: source.instance_id,
          name: '新分身',
        }),
      );
      groups = {
        ...groups,
        revision: 2,
        folders: { 原文件夹: installed.map((row) => row.instance_id) },
      };
      return result;
    }
    throw new Error('Unexpected request ' + path);
  };
  const app = await mount(t, 'Plugins', { api });
  app.label('筛选已安装插件').props.onClick();
  await app.flush();
  app.label('运行中').props.onClick();
  await app.flush();
  app.component('PluginFolderCard').props.onOpen();
  await app.flush();
  const manage = app.component('PluginManageCard');
  const card = await mount(t, 'PluginManageCard', {
    props: { plugin: manage.props.plugin },
    emit: (event, ...args) => {
      if (event === 'action') return manage.props.onAction(...args);
    },
  });
  card
    .nodes()
    .find((node) => node.type?.name === 'VListItem' && node.props.title === '分身')
    .props.onClick();
  await app.flush();
  const dialogNode = app.component('PluginCloneDialog');
  assert.equal(dialogNode.props.plugin.instance_id, source.instance_id);
  const dialog = await mount(t, 'PluginCloneDialog', {
    props: { plugin: dialogNode.props.plugin },
    api,
    emit: (event, value) =>
      event === 'created' ? dialogNode.props.onCreated(value) : dialogNode.props.onClose(),
  });
  await dialog.set('分身后缀', 'CopyA');
  const readsBefore = allRequests.length;
  await dialog.submit();
  await dialog.flush();
  await app.flush();
  assert.equal(app.component('PluginCloneDialog'), undefined);
  assert.deepEqual(
    allRequests.slice(readsBefore).map((row) => row.path),
    ['/plugins/SeedRescue2/clone', '/plugins?state=installed', '/plugins/market?details=true', '/plugins/statistic', '/plugins/organization'],
  );
  assert.equal(allRequests.filter((row) => row.body).length, 1);
  assert.deepEqual(allRequests.find((row) => row.body).body, { ...originalCloneDefaults, suffix: 'CopyA' });
  assert.deepEqual(
    app
      .nodes()
      .filter((node) => node.type?.name === 'PluginManageCard')
      .map((node) => node.props.plugin.instance_id),
    ['SeedRescue2', 'SeedRescue2copya'],
  );
  assert.ok(
    app
      .nodes()
      .some(
        (node) =>
          node.props?.class === 'plugin-folder-breadcrumb' && app.text(node).includes('原文件夹'),
      ),
  );
  assert.equal(app.toasts.at(-1), '插件分身已创建');
});

test('creation remains successful when the subsequent list refresh fails and never posts a second clone', async (t) => {
  const source = plugin();
  let created = false;
  const writes = [];
  const api = async (path, body) => {
    if (body) {
      writes.push({ path, body });
      created = true;
      return resultFor(source, body);
    }
    if (path === '/plugins?state=installed') {
      if (created) throw new APIError('READ_FAILED', '刷新失败，请稍后刷新列表', 'refresh-19');
      return [source];
    }
    if (path === '/plugins/organization') return { revision: 1, folders: {}, order: [] };
    throw new Error('Unexpected request ' + path);
  };
  const app = await mount(t, 'Plugins', { api });
  await app.component('PluginManageCard').props.onAction('clone', source);
  await app.flush();
  const dialogNode = app.component('PluginCloneDialog');
  const dialog = await mount(t, 'PluginCloneDialog', {
    props: { plugin: dialogNode.props.plugin },
    api,
    emit: (event, value) =>
      event === 'created' ? dialogNode.props.onCreated(value) : dialogNode.props.onClose(),
  });
  await dialog.set('分身后缀', 'CopyA');
  await dialog.submit();
  await dialog.flush();
  await app.flush();
  assert.equal(app.component('PluginCloneDialog'), undefined);
  assert.equal(app.toasts.at(-1), '插件分身已创建');
  assert.match(
    app.text(app.nodes().find((node) => node.props?.role === 'alert')),
    /刷新失败.*refresh-19/,
  );
  await dialog.submit();
  assert.equal(app.polls.length, 0);
  await app.flush();
  assert.equal(writes.length, 1);
});

const repositoryOffer = (overrides = {}) => plugin({
  plugin_id: 'OwnedFixture', instance_id: 'OwnedFixture', version: '1.0',
  has_update: true, repo_url: 'https://owned.invalid/index.json', repository_version: '2.0',
  available: true, ...overrides,
});
function updateDialog(app) {
  return app.component('PluginVersionHistoryDialog');
}
function installedAPI(source, updateResult = { success: true }) {
  return async (path) => {
    if (path === '/plugins?state=installed') return [source];
    if (path === '/plugins/organization') return { revision: 1, folders: {}, order: [] };
    if (path === '/plugins/install') return updateResult;
    throw new Error('Unexpected request ' + path);
  };
}

test('update menu requires an exact repository candidate and never treats display version as an update', async (t) => {
  for (const source of [
    plugin(), plugin({ has_update: true }),
    plugin({ update: { target_version: '2.0' } }),
    repositoryOffer({ source_instance_id: 'OwnedFixtureSource', has_update: false }),
  ]) {
    const card = await mount(t, 'PluginManageCard', { props: { plugin: source } });
    assert.equal(card.nodes().find((n) => n.type?.name === 'VListItem' && String(n.props.title).startsWith('更新至')), undefined);
  }
  const source = repositoryOffer({ display_version: 'display-99' });
  const card = await mount(t, 'PluginManageCard', { props: { plugin: source } });
  const item = card.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '更新至 v2.0');
  assert.ok(item);
  item.props.onClick();
  assert.equal(card.events[0][1], 'repository-update');
  assert.equal(card.events[0][2].instance_id, source.instance_id);
  const clone = repositoryOffer({ source_instance_id: 'OwnedFixtureSource', instance_id: 'OwnedFixtureqa1' });
  const cloneCard = await mount(t, 'PluginManageCard', { props: { plugin: clone } });
  cloneCard.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '更新至 v2.0').props.onClick();
  assert.equal(cloneCard.events[0][1], 'repository-update');
  assert.equal(cloneCard.events[0][2].instance_id, 'OwnedFixtureqa1');
  const unavailable = await mount(t, 'PluginManageCard', { props: { plugin: repositoryOffer({ available: false }) } });
  assert.equal(unavailable.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '更新至 v2.0').props.disabled, true);
});

test('pinned update action opens history, closes without writes, then updates latest without a second confirmation', async (t) => {
  const source = repositoryOffer({ display_version: 'display-only' });
  const app = await mount(t, 'Plugins', { api: installedAPI(source) });
  await app.component('PluginManageCard').props.onAction('repository-update', source);
  await app.flush();
  assert.ok(updateDialog(app));
  assert.equal(updateDialog(app).props['show-update-action'], true);
  assert.equal(app.nodes().some((node) => node.type?.name === 'VCardTitle' && app.text(node) === '确认更新'), false);
  await updateDialog(app).props.onClose();
  await app.flush();
  assert.equal(app.requests.filter((r) => r.body).length, 0);
  await app.component('PluginManageCard').props.onAction('repository-update', source);
  await app.flush();
  await updateDialog(app).props.onUpdate();
  await app.flush();
  const writes = app.requests.filter((r) => r.body);
  assert.deepEqual(writes, [{ path: '/plugins/install', body: {
    plugin_id: source.plugin_id, repo_url: source.repo_url, force: true,
  }, method: undefined }]);
  assert.equal(app.toasts.at(-1), source.name + '已更新');
  assert.equal(updateDialog(app), undefined);
});

test('incompatible catalog update remains disabled while installed settings stay usable', async (t) => {
  const source = repositoryOffer({ update_blocked_reason: '当前仓库版本不支持本机平台' });
  const card = await mount(t, 'PluginManageCard', { props: { plugin: source } });
  const update = card.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '更新至 v2.0');
  const settings = card.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '设置');
  assert.equal(update.props.disabled, true);
  assert.notEqual(settings.props.disabled, true);
  update.props.onClick();
  assert.deepEqual(card.events, []);
  assert.match(card.text(card.nodes()), /当前仓库版本不支持本机平台/);
  const app = await mount(t, 'Plugins', { api: installedAPI(source) });
  await app.component('PluginManageCard').props.onAction('repository-update', source);
  await app.flush();
  assert.equal(updateDialog(app), undefined);
  assert.equal(app.requests.filter((r) => r.body).length, 0);
});

test('retired migration envelopes cannot claim a repository update succeeded', async (t) => {
  const source = repositoryOffer();
  for (const result of [{ migration_committed: true, review_pending: true }, {}, { success: false, message: '制品校验失败' }]) {
    const app = await mount(t, 'Plugins', { api: installedAPI(source, result) });
    await app.component('PluginManageCard').props.onAction('repository-update', source);
    await app.flush();
    await updateDialog(app).props.onUpdate();
    await app.flush();
    assert.ok(updateDialog(app));
    assert.match(updateDialog(app).props['update-error'], /制品校验失败|插件更新失败/);
    assert.deepEqual(app.toasts, []);
    assert.equal(app.requests.filter((r) => r.body).length, 1);
  }
});

test('clone settings keep the exact instance and ignore retired review metadata', async (t) => {
  const source = plugin({ instance_id: 'OwnedFixturecopy', source_instance_id: 'OwnedFixture', update_review_pending: true });
  const card = await mount(t, 'PluginManageCard', { props: { plugin: source } });
  const item = card.nodes().find((n) => n.type?.name === 'VListItem' && n.props.title === '设置');
  assert.ok(item);
  item.props.onClick();
  assert.equal(card.events[0][1], 'settings');
  assert.equal(card.events[0][2].instance_id, source.instance_id);
  const app = await mount(t, 'Plugins', { api: installedAPI(source) });
  await app.component('PluginManageCard').props.onAction('settings', source);
  await app.flush();
  assert.equal(app.component('PluginConfigDialog').props.plugin.instance_id, source.instance_id);
  assert.equal(app.requests.filter((r) => r.body).length, 0);
});

test('retired restore and update actions cannot submit stale clone review snapshots', async (t) => {
  const retained = plugin({
    instance_id: 'OwnedFixtureheldcopy', source_instance_id: 'OwnedFixture', installed: false,
    restore_review: { version: '1.0', generation: 3, revision: 7 }, update_review_pending: true,
    available: false, unavailable_reason: '插件代码不可用或标准契约不匹配',
  });
  const app = await mount(t, 'Plugins', { api: installedAPI(retained) });
  const onAction = app.component('PluginManageCard').props.onAction;
  await onAction('restore-reviewed', retained);
  await onAction('update', retained);
  await onAction('install', retained);
  await app.flush();
  assert.equal(app.requests.filter((r) => r.body).length, 0);
  assert.deepEqual(app.toasts, []);
});

test('unavailable retained code disables ordinary install with actual code availability reason', async (t) => {
  const retained = plugin({
    installed: false, available: false, unavailable_reason: '插件代码不可用或标准契约不匹配',
    update_review_pending: true, restore_review_blocked_reason: 'retired migration field',
  });
  const card = await mount(t, 'PluginManageCard', { props: { plugin: retained } });
  const button = card.nodes().find((n) => n.type?.name === 'VBtn' && card.text(n).trim() === '安装');
  assert.ok(button);
  assert.equal(button.props.disabled, true);
  assert.equal(button.props.title, retained.unavailable_reason);
  assert.equal(card.nodes().find((n) => n.type?.name === 'VBtn' && card.text(n).includes('恢复')), undefined);
});
