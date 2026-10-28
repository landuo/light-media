import * as pluginProject from '../src/features/plugin-project.ts';
import * as pluginMarket from '../src/features/plugin-market.ts';
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
async function mount(t, { failSave = false, initialOrganization, installed } = {}) {
  const fixture = {
    organization: initialOrganization || fixtureOrganization(),
    requests: [],
    toasts: [],
    polls: [],
    failSave,
  };
  const mounted = [],
    activated = [],
    unmounted = [],
    module = { exports: {} };
  const clone = (value) => JSON.parse(JSON.stringify(value));
  new Function('require', 'exports', 'setInterval', 'clearInterval', compiled)(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          onMounted: (fn) => mounted.push(fn),
          onActivated: (fn) => activated.push(fn),
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
            if (path === '/plugins?state=installed') return installed || ['one', 'two', 'three'].map(plugin);
            if (path.startsWith('/plugins/market?')) return {items: (installed || []).filter(card => card.has_update), sources: []};
            if (path === '/plugins/statistic') return {};
            if (path === '/plugins/organization') {
              if (method === 'PUT') {
                if (fixture.failSave) throw new Error('保存失败');
                if (fixture.failFolders && body.folders) throw new Error('文件夹保存失败');
                assert.equal(body.revision, undefined, 'source independent writes do not require a revision');
                fixture.organization = { ...fixture.organization, ...clone(body), revision: fixture.organization.revision + 1 };
              }
              return clone(fixture.organization);
            }
            throw new Error('Unexpected API ' + path);
          },
          toast: (value) => fixture.toasts.push(value),
          errorText: (error) => error.message,
        };
      if (name === '../features/plugin-market') return pluginMarket;
    if (name === '../features/plugin-project') return pluginProject;
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
    async activate() { for (const fn of activated) await fn(); await flush(); },
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
  assert.deepEqual(organization.folderMembers(app.fixture.organization.folders['旧组']), ['one']);
  assert.equal(app.fixture.organization.folders['新组'].future, undefined, 'fixed view normalization drops unknown appearance fields');
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
  assert.equal(app.fixture.organization.folders['改名组'].future, undefined);
});

test('actual draggable actions save root order and folder membership once without background refresh during dragging', async (t) => {
  const app = await mount(t);
  await app.click('排序插件');
  let draggable = app.component('Draggable');
  assert.equal(draggable.props.disabled, false);
  const original = draggable.props.modelValue;
  draggable.props.onStart({ oldIndex: 1 });
  const reads = app.fixture.requests.length;
  assert.equal(app.fixture.polls.length, 0);
  await app.flush();
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
  assert.equal(app.writes().length, 3);
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
  assert.deepEqual(organization.folderMembers(app.fixture.organization.folders['旧组']), []);
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

test('installed filters match all 28 actual reference branches and keep folder counts scoped', () => {
  const ref = JSON.parse(
    readFileSync(new URL('./fixtures/plugin-installed-filter-reference.json', import.meta.url)),
  );
  assert.equal(ref.cases.length, 28);
  const installed = ref.plugins.map((source) => ({
    ...plugin(source.id),
    name: source.plugin_name,
    state: source.state,
    has_update: source.has_update,
  }));
  const groups = { revision: 1, folders: { 分组: ['one', 'two', 'three'], 空组: [] }, order: [] };
  for (const row of ref.cases) {
    const flags = { running: row.running, updates: row.updates };
    const matches = installed
      .filter((item) => organization.installedPluginMatches(item, row.query || '', flags))
      .map((item) => item.instance_id);
    assert.deepEqual(matches, row.ids, JSON.stringify(row));
    const inner = organization.organizationGrid(groups, installed, '分组', row.query || '', flags);
    assert.deepEqual(
      inner.map((item) => item.id),
      ['one', 'two', 'three'].filter((id) => row.ids.includes(id)),
    );
    const outer = organization.organizationGrid(groups, installed, '', row.query || '', flags);
    const folders = outer.filter((item) => item.type === 'folder');
    const count = row.ids.filter((id) => ['one', 'two', 'three'].includes(id)).length;
    assert.equal(folders.find((item) => item.id === '分组')?.count || 0, count);
    assert.equal(
      folders.some((item) => item.id === '空组'),
      !row.query && !row.running && !row.updates,
    );
    assert.deepEqual(
      outer.filter((item) => item.type === 'plugin').map((item) => item.id),
      ['four', 'five'].filter((id) => row.ids.includes(id)),
    );
  }
});

test('actual installed menu supports name choose/clear, quick-filter precedence, IME enter and close', async (t) => {
  const installed = [
    { ...plugin('one'), name: 'Alpha', state: true, has_update: false },
    { ...plugin('two'), name: 'Beta', state: false, has_update: true },
    { ...plugin('three'), name: 'Gamma', state: true, has_update: true },
  ];
  const app = await mount(t, { installed });
  const rootIDs = () => app.component('Draggable')?.props.modelValue.map((item) => item.id) || [];
  await app.click('筛选已安装插件');
  let input = app.label('插件名称');
  assert.deepEqual(input.props.items, ['Alpha', 'Beta', 'Gamma']);
  assert.ok(Object.hasOwn(input.props, 'clearable'));
  assert.equal(input.props.variant, 'outlined');
  input.props['onUpdate:modelValue']('ALPHA');
  await app.flush();
  assert.deepEqual(rootIDs(), ['旧组']);
  input = app.label('插件名称');
  input.props.onKeyup({ key: 'Enter', isComposing: true });
  await app.flush();
  assert.ok(app.label('插件名称'), 'IME enter must leave the menu open');
  app.label('插件名称').props.onKeyup({ key: 'Enter', isComposing: false });
  await app.flush();
  assert.equal(app.label('插件名称'), undefined);
  await app.click('筛选已安装插件');
  await app.click('运行中');
  assert.equal(app.label('插件名称'), undefined, 'quick choice closes the menu');
  assert.deepEqual(rootIDs(), ['three', '旧组'], 'running overrides ALPHA');
  await app.click('筛选已安装插件');
  await app.click('有新版本');
  assert.deepEqual(rootIDs(), ['three'], 'running and update are AND');
  await app.click('筛选已安装插件');
  await app.click('运行中');
  assert.deepEqual(rootIDs(), ['新组', 'three'], 'update alone overrides name');
  await app.click('筛选已安装插件');
  await app.click('有新版本');
  assert.deepEqual(rootIDs(), ['旧组'], 'name remains when quick filters are cleared');
  await app.click('筛选已安装插件');
  app.label('插件名称').props['onUpdate:modelValue'](null);
  await app.flush();
  assert.deepEqual(rootIDs(), ['新组', 'three', '旧组']);
  assert.equal(app.writes().length, 0, 'filtering never saves the organization or plugin config');
});

test('installed filters apply inside a folder and keeps the selected filters without periodic refresh', async (t) => {
  const installed = [
    { ...plugin('one'), state: false },
    { ...plugin('two'), state: true },
    { ...plugin('three'), state: true },
  ];
  const app = await mount(t, {
    installed,
    initialOrganization: { revision: 1, folders: { 全部: ['one', 'two'] }, order: [] },
  });
  app
    .nodes()
    .find((node) => node.type?.name === 'PluginFolderCard')
    .props.onOpen();
  await app.flush();
  await app.click('筛选已安装插件');
  await app.click('运行中');
  const ids = () => app.component('Draggable')?.props.modelValue.map((item) => item.id) || [];
  assert.deepEqual(ids(), ['two']);
  assert.equal(app.fixture.polls.length, 0);
  await app.flush();
  await app.flush();
  assert.deepEqual(ids(), ['two']);
  assert.equal(app.writes().length, 0);
});

test('mixed display sort keeps a confirmed user order when the separate folders write fails', async (t) => {
  const app = await mount(t);
  await app.click('排序插件');
  const draggable = app.component('Draggable');
  draggable.props.onStart({ oldIndex: 1 });
  draggable.props['onUpdate:modelValue']([...draggable.props.modelValue].reverse());
  await app.flush();
  app.fixture.failFolders = true;
  await app.component('Draggable').props.onEnd();
  await app.flush();
  assert.equal(app.writes().length, 2);
  assert.deepEqual(Object.keys(app.writes()[0].body), ['order']);
  assert.deepEqual(Object.keys(app.writes()[1].body), ['folders']);
  assert.deepEqual(app.fixture.organization.order.map(item => item.id), ['旧组', 'three', '新组']);
  assert.equal(app.fixture.organization.folders['新组'].order, 0);
  assert.deepEqual(app.fixture.toasts, []);
});

// Expected values come from executing the seven unmodified functions at the
// fixed MoviePilot frontend commit, not from the target component above.
const organizationOracle = JSON.parse(readFileSync(new URL('../../testdata/standard-organization/source-oracle.json', import.meta.url)));
test('fixed view load/order/quick-filter oracle is preserved', () => {
  const find=name=>organizationOracle.cases.find(c=>c.name===name);
  const load=find('legacy-load-defaults');
  assert.deepEqual(organization.normalizePluginFolders(load.input.folders), load.expected.folders);
  const order=find('legacy-user-order');
  assert.deepEqual(organization.normalizedPluginOrder({folders:{},order:order.input.order}),order.expected.order);
  const filter=find('combined-quick-filters-ignore-query');
  const result=filter.input.plugins.filter(p=>organization.installedPluginMatches({...plugin(p.id),name:p.plugin_name,state:p.state,has_update:p.has_update},filter.input.query,filter.input));
  assert.deepEqual(result,filter.expected.result);
});

test('fixed mixed-sort and filtered-folder-sort oracle reaches actual component writes', async t => {
  for (const name of ['root-mixed-folder-orders','filtered-folder-sort-drops-hidden-members']) {
    await t.test(name,async t=>{
      const sample=organizationOracle.cases.find(c=>c.name===name), input=sample.input;
      const ids=new Set([...Object.values(input.folders).flatMap(organization.folderMembers),...(input.plugins||[]).map(p=>p.id)]);
      const app=await mount(t,{initialOrganization:{revision:1,folders:input.folders,order:input.order},installed:[...ids].map(plugin)});
      if(input.current){app.nodes().find(n=>n.type?.name==='PluginFolderCard'&&n.props.name===input.current).props.onOpen();await app.flush();}
      await app.click('排序插件');
      const grid=app.component('Draggable');grid.props.onStart({oldIndex:0});
      const lookup=new Map(grid.props.modelValue.map(x=>[x.id,x]));
      grid.props['onUpdate:modelValue']((input.mixed||input.sorted).map(x=>lookup.get(x.id)));
      await app.flush();await app.component('Draggable').props.onEnd();await app.flush();
      const calls=app.writes().map(x=>({path:x.body.order?'/user/config/PluginOrder':'plugin/folders',value:x.body.order||x.body.folders}));
      assert.deepEqual(calls,sample.expected.calls);
    });
  }
});

test('fixed drag membership removes only first duplicate and preserves destination duplicate guard', () => {
  const sample=organizationOracle.cases.find(c=>c.name==='drop-removes-first-other-membership');
  const value={revision:1,folders:organization.normalizePluginFolders(sample.input.folders),order:[]};
  const moved=organization.moveToFolder(value,sample.input.dragged,sample.input.target);
  assert.deepEqual(moved.folders,sample.expected.folders);
  assert.throws(()=>organization.moveToFolder(moved,sample.input.dragged,sample.input.target),/已在此/);
  assert.deepEqual(value.folders.A.plugins,['one','one','two']);
});

test('fixed second-write failure keeps optimistic folders and already committed order', async t => {
  const sample=organizationOracle.cases.find(c=>c.name==='root-sort-second-write-fails'), input=sample.input;
  const app=await mount(t,{initialOrganization:{revision:1,folders:input.folders,order:input.order},installed:input.plugins.map(p=>plugin(p.id))});
  await app.click('排序插件');const grid=app.component('Draggable');grid.props.onStart({oldIndex:0});
  const lookup=new Map(grid.props.modelValue.map(x=>[x.id,x]));grid.props['onUpdate:modelValue'](input.mixed.map(x=>lookup.get(x.id)));
  app.fixture.failFolders=true;await app.flush();await app.component('Draggable').props.onEnd();await app.flush();
  assert.deepEqual(app.fixture.organization.order,sample.expected.order);
  for(const name of Object.keys(sample.expected.folders)){
    const node=app.nodes().find(x=>x.type?.name==='PluginFolderCard'&&x.props.name===name);
    assert.deepEqual(node.props.config,sample.expected.folders[name]);
  }
  assert.equal(app.fixture.organization.folders.A.order,0,'failed folders write did not commit');
  assert.deepEqual(app.fixture.toasts,[]);
});

test('fixed keep-alive return retains filters and folder while refreshing once, initial activation and sorting do not refresh', async t=>{
 const app=await mount(t);const count=app.fixture.requests.length;
 await app.activate();assert.equal(app.fixture.requests.length,count,'initial activation belongs to mounted load');
 app.nodes().find(n=>n.type?.name==='PluginFolderCard'&&n.props.name==='旧组').props.onOpen();await app.flush();
 await app.click('筛选已安装插件');app.label('插件名称').props['onUpdate:modelValue']('one');await app.flush();
 const reads=app.fixture.requests.length;await app.activate();
 assert.equal(app.fixture.requests.length,reads+4);
 assert.deepEqual(app.component('Draggable').props.modelValue.map(x=>x.id),['one']);
 assert.ok(app.nodes().some(n=>n.children==='旧组'));
 await app.click('排序插件');const sorting=app.fixture.requests.length;await app.activate();assert.equal(app.fixture.requests.length,sorting);
 const host=readFileSync(new URL('../src/App.vue',import.meta.url),'utf8');
 assert.match(host,/<KeepAlive>\s*<Plugins v-if="pageKey === 'plugins'"/);
});
