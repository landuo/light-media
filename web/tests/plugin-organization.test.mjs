import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as organization from '../src/features/plugin-organization.ts';

const { descriptor } = parse(
  readFileSync(new URL('../src/components/Plugins.vue', import.meta.url), 'utf8'),
);
const compiled = ts.transpileModule(
  compileScript(descriptor, { id: 'Plugins', inlineTemplate: true }).content,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;
const plugin = (id) => ({
  plugin_id: id,
  instance_id: id,
  name: id,
  description: 'fixture',
  version: '1.0',
  author: 'fixture',
  author_url: 'https://example.invalid',
  installed: true,
  state: false,
});
const fixtureOrganization = () => ({
  revision: 7,
  folders: {
    旧组: ['one'],
    新组: {
      plugins: ['two'],
      order: 0,
      showIcon: false,
      icon: 'mdi-folder-music',
      future: { zero: 0 },
    },
  },
  order: [
    { type: 'folder', id: '新组', order: 0 },
    { type: 'plugin', id: 'three', order: 1 },
    { type: 'folder', id: '旧组', order: 2 },
  ],
});
async function mount(t, { failSave = false, initialOrganization } = {}) {
  const fixture = {
    organization: initialOrganization || fixtureOrganization(),
    requests: [],
    toasts: [],
    polls: [],
    failSave,
  };
  const mounted = [],
    unmounted = [],
    module = { exports: {} };
  const clone = (value) => JSON.parse(JSON.stringify(value));
  new Function('require', 'exports', 'setInterval', 'clearInterval', compiled)(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          onMounted: (fn) => mounted.push(fn),
          onUnmounted: (fn) => unmounted.push(fn),
          defineAsyncComponent: () => ({ name: 'AsyncDialog' }),
          withDirectives: (node) => node,
        };
      if (name === '../api')
        return {
          api: async (path, body, method) => {
            fixture.requests.push({
              path,
              body: body === undefined ? undefined : clone(body),
              method,
            });
            if (path === '/plugins') return ['one', 'two', 'three'].map(plugin);
            if (path === '/plugins/market') return [];
            if (path === '/plugins/organization') {
              if (method === 'PUT') {
                if (fixture.failSave) throw new Error('保存失败');
                assert.equal(body.revision, fixture.organization.revision);
                fixture.organization = { ...clone(body), revision: body.revision + 1 };
              }
              return clone(fixture.organization);
            }
            throw new Error('Unexpected API ' + path);
          },
          toast: (value) => fixture.toasts.push(value),
          errorText: (error) => error.message,
        };
      if (name === '../features/plugin-organization') return organization;
      if (name === 'vuedraggable') return { name: 'Draggable' };
      if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
      if (name.endsWith('.vue')) return { name: name.split('/').at(-1).replace('.vue', '') };
      throw new Error('Unexpected dependency ' + name);
    },
    module.exports,
    (fn) => {
      fixture.polls.push(fn);
      return 1;
    },
    () => {},
  );
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {}, emit() {} }));
  t.after(() => {
    unmounted.forEach((fn) => fn());
    scope.stop();
  });
  for (const fn of mounted) await fn();
  const flush = async () => {
    for (let i = 0; i < 5; i++) await vue.nextTick();
  };
  await flush();
  const nodes = () => {
    const all = [];
    function visit(node) {
      if (!node) return;
      if (Array.isArray(node)) return node.forEach(visit);
      if (typeof node !== 'object') return;
      all.push(node);
      if (node.type?.name === 'Draggable')
        for (const element of node.props.modelValue) visit(node.children.item({ element }));
      else if (node.children?.default) visit(node.children.default({ props: {} }));
      else visit(node.children);
    }
    visit(render({}, []));
    return all;
  };
  const component = (name) => nodes().find((node) => node.type?.name === name);
  const label = (value) => nodes().find((node) => node.props?.['aria-label'] === value);
  const button = (text) =>
    nodes().find(
      (node) =>
        node.type?.name === 'VBtn' &&
        node.children
          ?.default?.()
          .some?.((child) => typeof child.children === 'string' && child.children.trim() === text),
    );
  const writes = () => fixture.requests.filter((row) => row.method === 'PUT');
  return {
    fixture,
    flush,
    nodes,
    component,
    label,
    button,
    writes,
    async click(value) {
      const node = label(value);
      assert.ok(node, value);
      await node.props.onClick();
      await flush();
    },
    editor: () => nodes().find((node) => node.props?.mode),
    async saveEditor(name, config = {}) {
      const node = nodes().find((node) => node.props?.mode);
      assert.ok(node);
      await node.props.onSave({ name, config });
      await flush();
    },
  };
}

test('folder create validates names, preserves both source formats and saves without touching plugin config', async (t) => {
  const app = await mount(t);
  await app.click('新建文件夹');
  await app.saveEditor('  ');
  assert.equal(app.writes().length, 0);
  assert.match(app.editor().props.error, /不能为空/);
  await app.saveEditor('旧组');
  assert.equal(app.writes().length, 0);
  assert.match(app.editor().props.error, /已存在/);
  await app.saveEditor('新增组');
  assert.equal(app.editor(), undefined);
  assert.equal(app.writes().length, 1);
  assert.deepEqual(app.fixture.organization.folders['旧组'], ['one']);
  assert.deepEqual(app.fixture.organization.folders['新组'].future, { zero: 0 });
  assert.equal(app.fixture.organization.folders['新组'].showIcon, false);
  assert.deepEqual(app.fixture.organization.folders['新增组'].plugins, []);
  assert.equal(
    app.fixture.requests.some((row) => row.path.endsWith('/config')),
    false,
  );
});

test('rename and appearance keep membership and unknown fields; failed saves retain the dialog and committed grid', async (t) => {
  const app = await mount(t);
  const folder = app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard' && node.props.name === '新组');
  folder.props.onRename();
  await app.flush();
  await app.saveEditor('改名组');
  assert.equal(app.fixture.organization.folders['新组'], undefined);
  assert.deepEqual(app.fixture.organization.folders['改名组'].plugins, ['two']);
  assert.equal(app.fixture.organization.order[0].id, '新组');
  const renamed = app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard' && node.props.name === '改名组');
  renamed.props.onAppearance();
  await app.flush();
  app.fixture.failSave = true;
  await app.saveEditor('改名组', {
    showIcon: true,
    background: 'https://example.invalid/image.png',
  });
  assert.ok(app.editor());
  assert.equal(app.editor().props.error, '保存失败');
  assert.equal(app.fixture.organization.folders['改名组'].showIcon, false);
  app.fixture.failSave = false;
  await app.saveEditor('改名组', { showIcon: true });
  assert.deepEqual(app.fixture.organization.folders['改名组'].plugins, ['two']);
  assert.deepEqual(app.fixture.organization.folders['改名组'].future, { zero: 0 });
});

test('actual draggable actions save root order and folder membership once while polling leaves the drag alone', async (t) => {
  const app = await mount(t);
  await app.click('排序插件');
  let draggable = app.component('Draggable');
  assert.equal(draggable.props.disabled, false);
  const original = draggable.props.modelValue;
  draggable.props.onStart({ oldIndex: 1 });
  const reads = app.fixture.requests.length;
  await app.fixture.polls[0]();
  assert.equal(app.fixture.requests.length, reads);
  draggable.props['onUpdate:modelValue']([...original].reverse());
  await app.flush();
  await app.component('Draggable').props.onEnd();
  await app.flush();
  assert.deepEqual(
    app.fixture.organization.order.map((item) => item.id),
    ['旧组', 'three', '新组'],
  );
  draggable = app.component('Draggable');
  draggable.props.onStart({ oldIndex: 1 });
  const target = app.nodes().find((node) => node.props?.['data-folder-id'] === '新组');
  await target.props.onDrop();
  await app.component('Draggable').props.onEnd();
  await app.flush();
  assert.equal(app.writes().length, 2);
  assert.deepEqual(app.fixture.organization.folders['新组'].plugins, ['two', 'three']);
  assert.equal(
    app
      .component('Draggable')
      .props.modelValue.some((item) => item.type === 'plugin' && item.id === 'three'),
    false,
  );
  assert.equal(
    app.fixture.organization.order.some((item) => item.type === 'plugin' && item.id === 'three'),
    true,
  );
});

test('folder members retain original order, removing one returns it to root and delete needs its own confirmation', async (t) => {
  const app = await mount(t);
  const folder = app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard' && node.props.name === '旧组');
  folder.props.onOpen();
  await app.flush();
  const member = app.component('PluginManageCard');
  assert.equal(member.props.plugin.instance_id, 'one');
  assert.equal(member.props['in-folder'], true);
  await member.props.onAction('remove-folder', member.props.plugin);
  await app.flush();
  assert.deepEqual(app.fixture.organization.folders['旧组'], []);
  const back = app
    .nodes()
    .find(
      (node) => node.type?.name === 'VBtn' && node.props?.['prepend-icon'] === 'mdi-arrow-left',
    );
  back.props.onClick();
  await app.flush();
  const root = app
    .nodes()
    .find(
      (node) => node.type?.name === 'PluginManageCard' && node.props.plugin.instance_id === 'one',
    );
  assert.ok(root);
  const newFolder = app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard' && node.props.name === '新组');
  const count = app.writes().length;
  newFolder.props.onDelete();
  await app.flush();
  assert.equal(app.writes().length, count);
  app.button('取消').props.onClick();
  await app.flush();
  assert.equal(app.writes().length, count);
  newFolder.props.onDelete();
  await app.flush();
  await app.button('确认').props.onClick();
  await app.flush();
  assert.equal(app.writes().length, count + 1);
  assert.equal(app.fixture.organization.folders['新组'], undefined);
  assert.ok(
    app
      .nodes()
      .some(
        (node) => node.type?.name === 'PluginManageCard' && node.props.plugin.instance_id === 'two',
      ),
  );
  assert.equal(
    app.fixture.requests.some((row) => row.path.endsWith('/uninstall')),
    false,
  );
});

test('sorting inside a folder saves original fractional global order and retains its appearance', async (t) => {
  const initialOrganization = fixtureOrganization();
  initialOrganization.folders['新组'].plugins = ['two', 'three'];
  const app = await mount(t, { initialOrganization });
  app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard' && node.props.name === '新组')
    .props.onOpen();
  await app.flush();
  await app.click('排序插件');
  const draggable = app.component('Draggable');
  draggable.props.onStart({ oldIndex: 0 });
  draggable.props['onUpdate:modelValue']([...draggable.props.modelValue].reverse());
  await app.flush();
  await app.component('Draggable').props.onEnd();
  await app.flush();
  assert.deepEqual(app.fixture.organization.folders['新组'].plugins, ['three', 'two']);
  assert.equal(app.fixture.organization.folders['新组'].showIcon, false);
  const indices = new Map(app.fixture.organization.order.map((item) => [item.id, item.order]));
  assert.equal(indices.get('three'), 0.1);
  assert.equal(indices.get('two'), 0.11);
});

test('legacy order, empty folders, literal hostile names and background URLs do not become executable content', () => {
  const value = JSON.parse(
    '{"revision":0,"folders":{"__proto__":["one"],"<img src=x onerror=bad()>":[]},"order":["three","one"]}',
  );
  const grid = organization.organizationGrid(value, ['one', 'three'].map(plugin), '', '');
  assert.equal(grid[0].id, 'three');
  assert.equal(
    grid.some((item) => item.id === '__proto__'),
    true,
  );
  assert.equal(organization.safeFolderBackground('javascript:alert(1)'), '');
  assert.equal(organization.safeFolderBackground('https://u:p@example.invalid/x'), '');
  assert.equal(
    organization.safeFolderBackground('https://example.invalid/x'),
    'https://example.invalid/x',
  );
});
