import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as historyFeatures from '../src/features/transfer-history.ts';
import { deleteHistoryBatch } from '../src/features/history-delete.ts';

const { descriptor } = parse(
  readFileSync(new URL('../src/components/TransferHistory.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'history-component' });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const template = compileTemplate({
  source: descriptor.template.content,
  filename: 'TransferHistory.vue',
  id: 'history-component',
  compilerOptions: { bindingMetadata: script.bindings },
});
assert.deepEqual(template.errors, []);
const compiledTemplate = ts.transpileModule(template.code, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function collectNodes(root) {
  const nodes = [];
  function visit(node) {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) return node.forEach(visit);
    nodes.push(node);
    // Execute real compiled SFC default/append slots through component mocks;
    // scoped table slots still receive representative rows explicitly below.
    if (Array.isArray(node.children)) visit(node.children);
    else if (node.children && node.type?.name !== 'VDataTableVirtual') {
      for (const key of ['default', 'append']) {
        if (typeof node.children[key] === 'function') visit(node.children[key]());
      }
    }
  }
  visit(root);
  return nodes;
}
function text(node) {
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(text).join('');
  if (node && typeof node === 'object') {
    if (typeof node.children?.default === 'function') return text(node.children.default());
    return text(node.children);
  }
  return '';
}
function invoke(handler, ...args) {
  for (const fn of Array.isArray(handler) ? handler : [handler]) fn(...args);
}

function mount(t) {
  const requests = [],
    toasts = [],
    timers = new Map(),
    listeners = new Set();
  let mounted,
    unmounted,
    nextTimer = 2;
  const location = { hash: '#history' };
  const urls = [];
  const scope = vue.effectScope();
  const module = { exports: {} };
  const events = {
    addEventListener(name, handler) {
      listeners.add(handler);
    },
    removeEventListener(name, handler) {
      listeners.delete(handler);
    },
  };
  const window = {
    ...events,
    setInterval(fn, delay) {
      timers.set(1, { fn, delay });
      return 1;
    },
    clearInterval(id) {
      timers.delete(id);
    },
    setTimeout(fn, delay) {
      const id = nextTimer++;
      timers.set(id, { fn, delay });
      return id;
    },
    confirm() {
      throw new Error('batch reorganization must open its form without an extra confirmation');
    },
  };
  const display = { mdAndUp: vue.ref(true) };
  const requireMock = (name) => {
    if (name === 'vuetify') return { useDisplay: () => display };
    if (name === 'vue')
      return {
        ...vue,
        withDirectives: (node) => node,
        onMounted(fn) {
          mounted = fn;
        },
        onUnmounted(fn) {
          unmounted = fn;
        },
      };
    if (name === '../features/transfer-history') return historyFeatures;
    if (name === '../features/history-delete') return { deleteHistoryBatch };
    if (name === '../api')
      return {
        api: (path, body, method, signal) =>
          new Promise((resolve, reject) =>
            requests.push({ path, body, method, signal, resolve, reject }),
          ),
        errorText: (error) => error.message,
        bytes: (value) => String(value),
        date: (value) => value,
        label: (value) => value,
        toast: (...args) => toasts.push(args),
      };
    if (name === './ReorganizeDialog.vue') return { default: { name: 'ReorganizeDialog' } };
    if (name === 'vuetify/components/VDataTable')
      return { VDataTableVirtual: { name: 'VDataTableVirtual' } };
    if (name === 'vuetify/components/VCheckbox') return { VCheckboxBtn: { name: 'VCheckboxBtn' } };
    const components = {
      VSelect: ['VSelect'],
      VCombobox: ['VCombobox'],
      VBottomSheet: ['VBottomSheet'],
      VDialog: ['VDialog'],
      VBtn: ['VBtn'],
      VTabs: ['VTabs', 'VTab'],
      VCard: ['VCard', 'VCardItem', 'VCardTitle', 'VCardText'],
      VWindow: ['VWindow', 'VWindowItem'],
      VList: ['VList', 'VListItem', 'VListItemTitle', 'VListItemSubtitle'],
      VChip: ['VChip'],
      VIcon: ['VIcon'],
      VDivider: ['VDivider'],
      VProgressLinear: ['VProgressLinear'],
    };
    if (name.startsWith('vuetify/components/') && components[name.split('/').at(-1)]) {
      return Object.fromEntries(components[name.split('/').at(-1)].map((name) => [name, { name }]));
    }
    throw new Error('Unexpected history component dependency: ' + name);
  };
  new Function(
    'require',
    'exports',
    'location',
    'history',
    'window',
    'document',
    'clearTimeout',
    compiled,
  )(
    requireMock,
    module.exports,
    location,
    {
      pushState(_state, _unused, hash) {
        location.hash = hash;
        urls.push(hash);
      },
    },
    window,
    events,
    (id) => timers.delete(id),
  );
  const app = scope.run(() => module.exports.default.setup({}, { expose() {}, emit() {} }));
  const renderModule = { exports: {} };
  new Function('require', 'exports', compiledTemplate)(requireMock, renderModule.exports);
  const nodes = () => collectNodes(renderModule.exports.render({}, [], {}, vue.proxyRefs(app)));
  const control = (name) => nodes().find((node) => node.type?.name === name);
  const table = () => control('VDataTableVirtual');
  const slot = (name, props) => collectNodes(table().children[name](props));
  const unmount = () => unmounted();
  t.after(() => {
    unmount();
    scope.stop();
  });
  mounted();
  return {
    app,
    display,
    requests,
    toasts,
    timers,
    listeners,
    location,
    urls,
    nodes,
    control,
    table,
    slot,
    unmount,
    flushSearch() {
      const entry = [...timers.entries()].find(([id]) => id !== 1);
      if (!entry) return;
      timers.delete(entry[0]);
      entry[1].fn();
    },
    tick: () => timers.get(1)?.fn(),
  };
}

async function settle() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}
const row = (id, status = 'pending') => ({
  id,
  file_id: 'execution-' + id,
  source: '/source/' + id + '.mkv',
  target: '/library/' + id + '.mkv',
  media: { title: id, media_type: 'movie' },
  status,
});
async function initial(h, items = []) {
  h.requests[0].resolve({ items, total: items.length });
  await settle();
}
const queue = (status) => ({
  groups: [
    {
      id: 'job-one',
      media: { title: 'Movie', year: 2026 },
      files: [{ id: 'execution-one', source: '/source/one.mkv', status }],
    },
  ],
});

test('history renders outlined Vuetify search/page controls and a virtual table without native field wrappers', async (t) => {
  const h = mount(t);
  await initial(h, [row('one', 'success')]);
  const search = h.control('VCombobox');
  const pageSize = h.control('VSelect');
  for (const node of [search, pageSize]) {
    assert.equal(node.props.variant, 'outlined');
    assert.equal(node.props.density, 'compact');
    assert.equal(node.props['hide-details'], '');
  }
  assert.equal(search.props['model-value'], null, 'empty combobox value must remain null');
  assert.equal(search.props.label, '搜索');
  assert.deepEqual(search.props.items, ['失败', '成功', 'one']);
  assert.equal(pageSize.props.label, '每页条数');
  assert.equal(pageSize.props.modelValue, 50);
  assert.deepEqual(pageSize.props.items, [25, 50, 100, 500, 1000]);
  assert.equal(h.table().props['item-value'], 'id');
  assert.equal(h.table().props['fixed-header'], '');
  assert.equal(h.table().props['show-select'], '');
  assert.equal(
    h.nodes().some((node) => ['input', 'select', 'datalist', 'label', 'table'].includes(node.type)),
    false,
  );
  assert.equal(
    h.nodes().some((node) => node.type === 'h1' || node.type === 'h2'),
    false,
  );
  assert.doesNotMatch(
    descriptor.styles[0].content,
    /\.th-search(?::focus-within| input)|\.th-search\s*\{[^}]*border:/,
  );
});

test('Vuetify search handlers preserve IME, debounce, status hints, URL and independent request cancellation', async (t) => {
  const h = mount(t);
  await initial(h);
  const search = () => h.control('VCombobox').props;
  const opening = h.app.showQueue();
  await h.app.reorganized('preparation-one');
  h.tick();
  const queueRequest = h.requests[1];
  const preparationRequest = h.requests[2];
  invoke(search()['onUpdate:modelValue'], 'draft');
  assert.equal(h.timers.get(2).delay, 280);
  h.flushSearch();
  const obsolete = h.requests[3];
  invoke(search().onCompositionstart);
  assert.equal(obsolete.signal.aborted, true);
  invoke(search()['onUpdate:modelValue'], 'zhong');
  invoke(search().onKeydown, { key: 'Enter', isComposing: true });
  h.flushSearch();
  assert.equal(h.requests.length, 4, 'IME draft and Enter do not search');
  assert.equal(queueRequest.signal.aborted, false);
  assert.equal(preparationRequest.signal.aborted, false);
  invoke(search().onCompositionend, { target: { value: '中文*' } });
  h.flushSearch();
  assert.equal(new URLSearchParams(h.requests[4].path.split('?')[1]).get('q'), '中文*');
  assert.equal(new URLSearchParams(h.location.hash.split('?')[1]).get('search'), '中文*');
  obsolete.resolve({ items: [row('stale')], total: 1 });
  h.requests[4].resolve({ items: [row('fresh', 'success')], total: 1 });
  await settle();
  assert.equal(h.app.items.value[0].id, 'fresh');
  invoke(search()['onUpdate:modelValue'], '失败');
  h.flushSearch();
  assert.equal(new URLSearchParams(h.requests[5].path.split('?')[1]).get('status'), 'failed');
  invoke(search()['onUpdate:modelValue'], null);
  invoke(search()['onClick:clear']);
  assert.equal(h.requests[5].signal.aborted, true);
  assert.equal(search()['model-value'], null);
  assert.equal(new URLSearchParams(h.requests[6].path.split('?')[1]).get('q'), '');
  h.flushSearch();
  assert.equal(
    h.requests.length,
    7,
    'clear replaces its debounce with exactly one immediate request',
  );
  h.requests[5].resolve({ items: [], total: 0 });
  h.requests[6].resolve({ items: [], total: 0 });
  queueRequest.resolve(queue('pending'));
  preparationRequest.resolve({ job: { state: 'running' } });
  await opening;
  await settle();
});

test('virtual table sorting reuses title/season/episode order, and all field/action slots remain present', async (t) => {
  const h = mount(t);
  const rows = [
    {
      ...row('episode-10', 'success'),
      media: { title: 'Show', media_type: 'tv' },
      season: 1,
      episodes: [10],
      size: 10,
      mode: 'link',
      updated_at: '2026-11-01',
    },
    {
      ...row('episode-2', 'failed'),
      media: { title: 'Show', media_type: 'tv' },
      season: 1,
      episodes: [2],
      size: 2,
      mode: 'copy',
      updated_at: '2026-10-01',
      error: 'snapshot changed',
      category: '华语电影',
    },
  ];
  await initial(h, rows);
  for (const key of ['title', 'source', 'mode', 'size', 'updated_at', 'status']) {
    invoke(h.table().props['onUpdate:sortBy'], [{ key, order: 'asc' }]);
    const header = h.table().props.headers.find((header) => header.key === key);
    const actual = [...h.table().props.items].sort(header.sortRaw).map(({ id }) => id);
    assert.deepEqual(
      actual,
      historyFeatures.sortHistory(rows, key, false).map(({ id }) => id),
    );
    assert.equal(header.sortable, true);
    invoke(h.table().props['onUpdate:sortBy'], [{ key, order: 'desc' }]);
    assert.deepEqual(
      [...h.table().props.items].sort((a, b) => header.sortRaw(b, a)).map(({ id }) => id),
      historyFeatures.sortHistory(rows, key, true).map(({ id }) => id),
    );
  }
  assert.match(text(h.slot('item.title', { item: rows[1] })), /Show\s*S01E02.*电视剧.*华语电影/);
  assert.match(
    text(h.slot('item.source', { item: rows[1] })),
    /本地.*\/source\/episode-2.mkv.*本地.*\/library\/episode-2.mkv/s,
  );
  assert.match(text(h.slot('item.mode', { item: rows[1] })), /copy/);
  assert.match(text(h.slot('item.size', { item: rows[1] })), /2/);
  assert.match(text(h.slot('item.updated_at', { item: rows[1] })), /2026-10-01/);
  const status = h.slot('item.status', { item: rows[1] }).find((node) => node.type === 'span');
  assert.equal(status.props.title, 'snapshot changed');
  assert.match(text(status), /失败/);
  h.app.openMenu.value = rows[1].id;
  const actions = h.slot('item.actions', { item: rows[1] });
  const redo = actions.find(
    (node) => node.props?.role === 'menuitem' && text(node).includes('重新整理'),
  );
  invoke(redo.props.onClick);
  assert.deepEqual(h.app.reorganizeItems.value, [rows[1]]);
  h.app.openMenu.value = rows[1].id;
  const remove = h
    .slot('item.actions', { item: rows[1] })
    .find((node) => node.props?.role === 'menuitem' && text(node).includes('删除'));
  invoke(remove.props.onClick);
  assert.deepEqual(h.app.deleteItems.value, [rows[1]]);
});

test('Vuetify page-size, page/group selection and visible batch counts retain IDs across pages', async (t) => {
  const h = mount(t);
  const first = row('first', 'success');
  const second = row('second', 'failed');
  h.requests[0].resolve({ items: [first], total: 51 });
  await settle();
  const selectRow = h.slot('item.data-table-select', { item: first })[0];
  invoke(selectRow.props['onUpdate:modelValue'], true);
  h.app.changePage(2);
  h.requests[1].resolve({ items: [second], total: 51 });
  await settle();
  invoke(h.slot('header.data-table-select', {})[0].props['onUpdate:modelValue'], true);
  assert.deepEqual(h.app.selected.value, ['first', 'second']);
  assert.deepEqual(
    h.app.selectedItems.value.map(({ id }) => id),
    ['first', 'second'],
  );
  const batch = h.nodes().find((node) => node.props?.['aria-label'] === '批量删除');
  assert.match(text(batch), /批量删除（2）/);
  invoke(batch.props.onClick);
  assert.deepEqual(
    h.app.deleteItems.value.map(({ id }) => id),
    ['first', 'second'],
  );
  h.app.toggleGrouped();
  assert.deepEqual(h.table().props['group-by'], [{ key: 'historyGroup', order: false }]);
  const group = { value: 'second' };
  let toggled;
  const groupNodes = h.slot('group-header', {
    item: group,
    columns: Array(9),
    isGroupOpen: () => false,
    toggleGroup: (item) => {
      toggled = item;
    },
  });
  invoke(groupNodes.find((node) => node.type === 'button').props.onClick);
  assert.equal(toggled, group);
  const groupCheckbox = groupNodes.find((node) => node.type?.name === 'VCheckboxBtn');
  assert.equal(groupCheckbox.props['model-value'], true);
  invoke(groupCheckbox.props['onUpdate:modelValue'], false);
  assert.deepEqual(h.app.selected.value, ['first'], 'group deselect never clears another page');
  invoke(h.control('VSelect').props['onUpdate:modelValue'], 100);
  assert.equal(h.app.page.value, 1);
  assert.equal(typeof h.app.pageSize.value, 'number');
  const params = new URLSearchParams(h.requests[2].path.split('?')[1]);
  assert.equal(params.get('page_size'), '100');
  assert.equal(params.get('page'), '1');
  assert.equal(new URLSearchParams(h.location.hash.split('?')[1]).get('grouped'), 'true');
  h.requests[2].resolve({ items: [first, second], total: 2 });
  await settle();
  assert.deepEqual(h.app.selected.value, ['first']);
  h.location.hash = '#history?search=成功&itemsPerPage=25&currentPage=2&grouped=true';
  h.app.restoreURL();
  assert.equal(h.control('VCombobox').props['model-value'], '成功');
  assert.equal(h.control('VSelect').props.modelValue, 25);
  const restored = new URLSearchParams(h.requests[3].path.split('?')[1]);
  assert.equal(restored.get('status'), 'success');
  assert.equal(restored.get('page'), '2');
  h.requests[3].resolve({ items: [first], total: 51 });
  await settle();
  assert.deepEqual(h.app.selected.value, ['first']);
});

test('actual history SFC polls an open queue every three seconds without overlapping reads', async (t) => {
  const h = mount(t);
  await initial(h);
  assert.equal(h.timers.get(1).delay, 3000);
  h.tick();
  assert.equal(h.requests.length, 1, 'closed queue and finished history stay idle');
  const opening = h.app.showQueue();
  assert.equal(h.requests[1].path, '/history/transfers/queue');
  h.tick();
  h.tick();
  assert.equal(h.requests.length, 2, 'slow queue read is not duplicated');
  h.requests[1].resolve(queue('pending'));
  await opening;
  h.tick();
  h.requests[2].resolve(queue('success'));
  await settle();
  assert.equal(h.app.completedQueue.value, 1);
  h.app.queueOpen.value = false;
  h.tick();
  assert.equal(h.requests.length, 3, 'closing the queue stops its periodic reads');
});

test('actual history SFC polls pending history and retains deletion failures and selected IDs', async (t) => {
  const h = mount(t);
  const failed = row('failed', 'failed'),
    pending = row('pending');
  await initial(h, [failed, pending]);
  h.app.selected.value = [failed.id];
  h.app.openDelete([failed]);
  const deletion = h.app.remove(false, true);
  assert.equal(h.requests[1].method, 'DELETE');
  assert.equal(h.requests[1].body.file_id, failed.file_id);
  h.requests[1].reject(new Error('source snapshot changed'));
  await settle();
  h.requests[2].resolve({ items: [failed, pending], total: 2 });
  await deletion;
  const outcome = h.app.deleteError.value;
  assert.match(outcome, /已删除 0 条，1 条删除失败/);
  assert.match(outcome, /source snapshot changed/);
  h.tick();
  h.tick();
  assert.equal(h.requests.length, 4, 'pending history refresh is not duplicated');
  assert.match(h.requests[3].path, /capabilities=deferred/);
  h.requests[3].resolve({ items: [failed, row('pending', 'success')], total: 2 });
  await settle();
  assert.equal(h.app.deleteError.value, outcome);
  assert.deepEqual(h.app.selected.value, [failed.id]);
  assert.equal(h.slot('item.data-table-select', { item: failed })[0].props['model-value'], true);
  assert.match(
    text(h.nodes().find((node) => node.props?.['aria-label'] === '批量删除')),
    /批量删除（1）/,
  );
  assert.ok(
    h
      .nodes()
      .some(
        (node) => node.props?.role === 'alert' && text(node).includes('source snapshot changed'),
      ),
  );
  h.tick();
  assert.equal(h.requests.length, 4, 'history polling stops after pending rows finish');
});

test('background preparation is tracked without an open queue and refreshes history on completion', async (t) => {
  const h = mount(t);
  await initial(h);
  await h.app.reorganized('preparation-one');
  h.tick();
  h.tick();
  assert.equal(h.requests.length, 2);
  assert.equal(h.requests[1].path, '/jobs/preparation-one');
  h.requests[1].resolve({ job: { state: 'running' } });
  await settle();
  h.tick();
  h.requests[2].resolve({ job: { state: 'succeeded' } });
  await settle();
  assert.match(h.requests[3].path, /^\/history\/transfers\?/);
  h.requests[3].resolve({ items: [row('prepared')], total: 1 });
  await settle();
  h.tick();
  assert.equal(h.requests[4].path.startsWith('/history/transfers?'), true);
  assert.equal(h.requests.filter(({ path }) => path === '/jobs/preparation-one').length, 2);
  h.requests[4].resolve({ items: [row('prepared', 'success')], total: 1 });
  await settle();
});

test('failed preparation reports its reason and refreshes history without clearing deletion results', async (t) => {
  const h = mount(t);
  await initial(h);
  await h.app.reorganized('preparation-failed');
  h.app.deleteError.value = 'existing deletion outcome';
  h.app.selected.value = ['failed-selection'];
  h.tick();
  h.requests[1].resolve({ job: { state: 'failed', error_message: 'recognition failed' } });
  await settle();
  assert.deepEqual(h.toasts, [['重新整理准备未完成：recognition failed', true]]);
  h.requests[2].resolve({ items: [], total: 0 });
  await settle();
  assert.equal(h.app.deleteError.value, 'existing deletion outcome');
  assert.deepEqual(h.app.selected.value, ['failed-selection']);
  h.tick();
  assert.equal(h.requests.length, 3);
});

test('unmount clears the timer and listeners and aborts all polling reads', async (t) => {
  const h = mount(t);
  await initial(h, [row('pending')]);
  await h.app.reorganized('preparation-one');
  const opening = h.app.showQueue();
  h.tick();
  assert.equal(h.requests.length, 4);
  const snapshot = h.app.result.value;
  h.unmount();
  assert.equal(h.timers.size, 0);
  assert.equal(h.listeners.size, 0);
  for (const request of h.requests.slice(1)) assert.equal(request.signal.aborted, true);
  h.requests[1].resolve(queue('success'));
  h.requests[2].resolve({ job: { state: 'failed', error_message: 'late failure' } });
  h.requests[3].resolve({ items: [row('late', 'success')], total: 1 });
  await opening;
  await settle();
  assert.equal(h.app.result.value, snapshot);
  assert.deepEqual(h.app.queueGroups.value, []);
  assert.deepEqual(h.toasts, []);
  h.tick();
  await h.app.loadQueue();
  await h.app.load(true);
  assert.equal(h.requests.length, 4);
});

test('batch reorganization directly opens the shared form for exactly the chosen records', async (t) => {
  const h = mount(t);
  const rows = [row('one', 'success'), row('two', 'success')];
  await initial(h, rows);
  h.app.selected.value = rows.map(({ id }) => id);
  h.app.reorganize(rows);
  assert.deepEqual(h.app.reorganizeItems.value, rows);
  assert.deepEqual(h.app.selected.value, ['one', 'two']);
  assert.equal(h.requests.length, 1, 'opening the form does not submit organization');
});

const componentNodes = (h, name) => h.nodes().filter((node) => node.type?.name === name);
const buttonWithText = (h, title) =>
  componentNodes(h, 'VBtn').find((node) => text(node).trim() === title);

for (const [title, color, deleteSource, deleteTarget] of [
  ['仅删除整理记录', 'primary', false, false],
  ['删除整理记录和原文件', 'warning', true, false],
  ['删除媒体库文件和整理记录', 'info', false, true],
  ['删除媒体库文件、整理记录和原文件', 'error', true, true],
]) {
  test('rendered deletion scope submits once with exact identity: ' + title, async (t) => {
    const h = mount(t);
    const item = row('stable-record', 'success');
    await initial(h, [item]);
    invoke(h.slot('item.data-table-select', { item })[0].props['onUpdate:modelValue'], true);
    invoke(h.nodes().find((node) => node.props?.['aria-label'] === '批量删除').props.onClick);
    const sheet = h.control('VBottomSheet');
    assert.equal(sheet.props.inset, '');
    assert.equal(sheet.props['model-value'], true);
    assert.match(text(h.control('VCardTitle')), /确认删除 stable-record/);
    assert.equal(componentNodes(h, 'VBtn').filter((node) => node.props.color).length, 4);
    const button = buttonWithText(h, title);
    assert.equal(button.props.color, color);
    invoke(button.props.onClick);
    invoke(button.props.onClick);
    assert.equal(
      h.requests.length,
      2,
      'scope choice is the only confirmation; duplicate clicks are ignored',
    );
    assert.equal(h.control('VBottomSheet'), undefined);
    assert.equal(h.control('VDialog').props.persistent, '');
    assert.equal(h.control('VProgressLinear').props['model-value'], 0);
    assert.equal(h.requests[1].path, '/history/transfers/stable-record');
    assert.deepEqual(h.requests[1].body, {
      confirm: true,
      file_id: item.file_id,
      delete_source: deleteSource,
      delete_target: deleteTarget,
    });
    h.requests[1].resolve({ deleted: true });
    await settle();
    assert.equal(h.control('VProgressLinear').props['model-value'], 100);
    h.requests[2].resolve({ items: [], total: 0 });
    await settle();
    assert.equal(h.control('VDialog'), undefined);
    assert.deepEqual(h.app.selected.value, []);
  });
}

test('delete sheet dismisses without a request; partial batch failure remains visible and selected', async (t) => {
  const h = mount(t);
  const good = row('good', 'success'),
    bad = row('bad', 'failed');
  await initial(h, [good, bad]);
  h.app.openDelete([good, bad]);
  invoke(h.control('VBottomSheet').props['onUpdate:modelValue'], false);
  assert.equal(h.control('VBottomSheet'), undefined);
  assert.equal(h.requests.length, 1);
  h.app.openDelete([good, bad]);
  invoke(h.nodes().find((node) => node.props?.['aria-label'] === '关闭删除窗口').props.onClick);
  assert.equal(h.control('VBottomSheet'), undefined);
  h.app.selected.value = ['good', 'bad'];
  invoke(h.nodes().find((node) => node.props?.['aria-label'] === '批量删除').props.onClick);
  assert.match(text(h.control('VCardTitle')), /选中的 2 条整理记录/);
  invoke(buttonWithText(h, '删除媒体库文件、整理记录和原文件').props.onClick);
  h.requests[1].resolve({ deleted: true });
  await settle();
  assert.equal(h.control('VProgressLinear').props['model-value'], 50);
  h.requests[2].reject(new Error('target snapshot changed'));
  await settle();
  h.requests[3].resolve({ items: [bad], total: 1 });
  await settle();
  assert.deepEqual(h.app.selected.value, ['bad']);
  assert.match(
    text(h.nodes().find((node) => node.props?.role === 'alert')),
    /已删除 1 条，1 条删除失败.*target snapshot changed/s,
  );
  assert.match(
    text(h.nodes().find((node) => node.props?.['aria-label'] === '批量删除')),
    /批量删除（1）/,
  );
});

const mediaQueue = () => ({
  groups: [
    {
      id: 'job-a',
      media: { title: 'Show', year: 2026 },
      files: [
        {
          id: 'execution/a',
          history_id: 'stable-one',
          source: '/source/episode-1.mkv',
          status: 'pending',
          job_state: 'running',
          can_cancel: true,
          size: 42,
          progress: 25.5,
        },
      ],
    },
    {
      id: 'job-b',
      media: { title: 'Show', year: 2026 },
      files: [
        {
          id: 'execution-b',
          source: '/source/episode-2.mkv',
          status: 'success',
          can_cancel: true,
          progress: 100,
        },
      ],
    },
    {
      id: 'job-c',
      media: { title: 'Other', year: 2025 },
      files: [
        {
          id: 'execution-c',
          source: '/source/other.mkv',
          status: 'failed',
          can_cancel: false,
          error: 'recognition failed',
        },
      ],
    },
  ],
});

test('queue renders responsive Vuetify card, real media tabs, file states and progress without fake actions', async (t) => {
  const h = mount(t);
  await initial(h);
  invoke(h.nodes().find((node) => node.props?.['aria-label'] === '整理队列').props.onClick);
  assert.equal(h.control('VDialog').props['max-width'], '60rem');
  assert.equal(h.control('VDialog').props.scrollable, '');
  assert.equal(h.control('VDialog').props.fullscreen, false);
  assert.match(text(h.control('VCardText')), /加载中/);
  h.display.mdAndUp.value = false;
  assert.equal(h.control('VDialog').props.fullscreen, true);
  h.requests[1].resolve(mediaQueue());
  await settle();
  const tabs = componentNodes(h, 'VTab');
  assert.equal(tabs.length, 2, 'two executions of one actual media share a tab');
  assert.match(text(tabs[0]), /Show/);
  assert.ok(text(tabs[0]).includes('1 / 2'));
  assert.match(text(tabs[1]), /Other/);
  assert.ok(text(tabs[1]).includes('0 / 1'));
  assert.equal(h.control('VTabs').props['show-arrows'], '');
  assert.equal(h.control('VTabs').props.stacked, '');
  invoke(h.control('VTabs').props['onUpdate:modelValue'], tabs[1].props.value);
  assert.equal(h.control('VWindow').props.modelValue, tabs[1].props.value);
  assert.equal(h.control('VWindow').props.touch, false);
  assert.equal(componentNodes(h, 'VListItem').length, 3);
  assert.match(text(h.control('VCardText')), /文件大小：42/);
  assert.deepEqual(
    componentNodes(h, 'VChip').map((node) => text(node).trim()),
    ['正在整理', '完成', '失败'],
  );
  const progress = componentNodes(h, 'VProgressLinear');
  assert.equal(progress.length, 2, 'only real running file has per-file progress');
  assert.ok(Math.abs(progress[0].props['model-value'] - 100 / 3) < 1e-10);
  assert.equal(progress[1].props['model-value'], 25.5);
  const cancel = componentNodes(h, 'VBtn').filter((node) => node.props.icon === 'mdi-cancel');
  assert.deepEqual(
    cancel.map((node) => node.props.disabled),
    [false, true, true],
  );
  assert.equal(
    h.nodes().some((node) => node.type === 'progress'),
    false,
  );
  assert.equal(
    componentNodes(h, 'VBtn').some((node) => /暂停/.test(text(node))),
    false,
  );
  invoke(h.control('VDialog').props['onUpdate:modelValue'], false);
  assert.equal(h.control('VDialog'), undefined);
  h.tick();
  assert.equal(h.requests.length, 2, 'dismissal stops polling');
});

test('queue cancellation targets execution ID once and retains its failure across polling', async (t) => {
  const h = mount(t);
  await initial(h);
  const opening = h.app.showQueue();
  h.requests[1].resolve(mediaQueue());
  await opening;
  const cancel = componentNodes(h, 'VBtn').find(
    (node) => node.props['aria-label'] === '取消整理 episode-1.mkv',
  );
  invoke(cancel.props.onClick);
  invoke(cancel.props.onClick);
  assert.equal(h.requests.length, 3);
  assert.equal(h.requests[2].path, '/history/transfers/queue/execution%2Fa/cancel');
  assert.deepEqual(h.requests[2].body, {});
  assert.equal(
    componentNodes(h, 'VBtn').find((node) => node.props['aria-label'] === '取消整理 episode-1.mkv')
      .props.loading,
    true,
  );
  h.requests[2].reject(new Error('execution changed'));
  await settle();
  h.tick();
  h.requests[3].resolve(mediaQueue());
  await settle();
  assert.ok(
    h
      .nodes()
      .some((node) => node.props?.role === 'alert' && text(node).includes('execution changed')),
  );
  assert.equal(h.app.queueFiles.value.length, 3, 'failed cancellation does not remove files');
  const retry = componentNodes(h, 'VBtn').find(
    (node) => node.props['aria-label'] === '取消整理 episode-1.mkv',
  );
  invoke(retry.props.onClick);
  h.requests[4].resolve({ cancelled: true });
  await settle();
  const next = mediaQueue();
  next.groups[0].files[0].job_state = 'cancelled';
  next.groups[0].files[0].can_cancel = false;
  h.requests[5].resolve(next);
  await settle();
  h.requests[6].resolve({ items: [], total: 0 });
  await settle();
  assert.equal(h.app.queueActionError.value, '');
  assert.match(text(h.control('VCardText')), /已取消/);
  invoke(h.nodes().find((node) => node.props?.['aria-label'] === '关闭整理队列').props.onClick);
  assert.equal(h.control('VDialog'), undefined);
});

test('queue empty, preparation-only and failed read states use real card/list content', async (t) => {
  const h = mount(t);
  await initial(h);
  const opening = h.app.showQueue();
  h.requests[1].reject(new Error('queue unavailable'));
  await opening;
  assert.match(text(h.control('VCardText')), /queue unavailable/);
  assert.equal(h.control('VTabs'), undefined);
  h.tick();
  h.requests[2].resolve({
    groups: [],
    preparations: [{ id: 'prepare', paths: ['/source/batch'], state: 'running', history_count: 4 }],
  });
  await settle();
  assert.match(text(h.control('VList')), /重新整理 4 条记录/);
  assert.ok(text(h.control('VList')).includes('/source/batch'));
  assert.match(text(h.control('VChip')), /正在准备整理/);
  assert.equal(h.control('VTabs'), undefined);
  assert.doesNotMatch(text(h.control('VCardText')), /没有正在整理的任务/);
  h.tick();
  h.requests[3].resolve({ groups: [], preparations: [] });
  await settle();
  h.requests[4].resolve({ items: [], total: 0 });
  await settle();
  assert.match(text(h.control('VCardText')), /没有正在整理的任务/);
  assert.equal(h.control('VProgressLinear'), undefined);
});
