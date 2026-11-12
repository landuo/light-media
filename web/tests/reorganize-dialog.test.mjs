import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

// Execute the actual SFC render handlers and watchers without a browser or network.
const source = readFileSync(
  new URL('../src/components/ReorganizeDialog.vue', import.meta.url),
  'utf8',
);
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'organize-dialog-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function openDialog(t, initial = {}, options = {}) {
  const requests = [];
  let mounted = () => {},
    unmounted = () => {};
  const events = [];
  const toasts = [];
  const apiModule = {
    ui: {
      settings: {
        directories: [
          { library_path: '/library', mode: 'copy', scraping: false, library_type_folder: false },
        ],
      },
    },
    api: async (path, body, method, signal) => {
      requests.push({ path, body, ...(signal ? { signal } : {}) });
      if (options.api) return options.api(path, body, requests.length);
      return { items: [], total: 0, submitted: 0, failed: 0 };
    },
    label: (value) => value,
    errorText: (error) => error.message,
    image: (path) => path,
    toast: (message) => toasts.push(message),
  };
  if (options.noSettings) apiModule.ui.settings = null;
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api') return apiModule;
    if (name === '../features/bounded-read') return { boundedRead: (read, signal) => read(signal) };
    if (name === './FileManagerIcon.vue') return { default: {} };
    if (name === './DirectoryPathPicker.vue') return { name: 'DirectoryPathPicker' };
    if (/^vuetify\/components\/(VTextField|VSelect|VTextarea)$/.test(name)) {
      const component = name.split('/').at(-1);
      return { [component]: { name: component } };
    }
    if (name === 'vue')
      return {
        ...vue,
        onMounted: (fn) => {
          mounted = fn;
        },
        onUnmounted: (fn) => {
          unmounted = fn;
        },
        useId: () => 'organize-test',
        withDirectives: (vnode) => vnode,
      };
    throw new Error('Unexpected component dependency: ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(
      { paths: ['/downloads/Test.mkv'], historyIds: options.historyIds, initial },
      { expose: () => {}, emit: (...event) => events.push(event) },
    ),
  );
  t.after(() => scope.stop());
  function nodes() {
    const result = [];
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) return node.forEach(visit);
      result.push(node);
      if (node.children && !Array.isArray(node.children) && typeof node.children === 'object') {
        for (const slot of Object.values(node.children))
          if (typeof slot === 'function') visit(slot());
      } else visit(node.children);
    }
    visit(render({}, []));
    return result;
  }
  const pathInput = () => nodes().find((node) => node.props?.['aria-label'] === '目的路径');
  const selects = () => nodes().filter((node) => node.type?.name === 'VSelect');
  const switches = () => nodes().filter((node) => node.props?.type === 'checkbox');
  const previewButton = () => nodes().find((node) => node.props?.class === 'preview-action');
  return {
    requests,
    ui: apiModule.ui,
    mount: () => mounted(),
    unmount: () => unmounted(),
    events,
    toasts,
    nodes,
    pathInput,
    selects,
    switches,
    previewButton,
    async browsePath(value) {
      const picker = nodes().find((node) => node.type?.name === 'DirectoryPathPicker');
      picker.props['onUpdate:modelValue'](value);
      await vue.nextTick();
    },
    async path(value) {
      pathInput().props['onUpdate:modelValue'](value);
      await vue.nextTick();
    },
    async select(index, value) {
      selects()[index].props['onUpdate:modelValue'](value);
      await vue.nextTick();
    },
    async toggle(index, checked) {
      switches()[index].props.onChange({ target: { checked } });
      await vue.nextTick();
    },
    async preview() {
      await previewButton().props.onClick();
      await vue.nextTick();
      return requests.at(-1).body;
    },
    async organize(background = false) {
      await nodes()
        .find((node) =>
          background
            ? node.props?.class === 'queue-action'
            : node.type === 'button' && renderedText(node).includes('立即整理'),
        )
        .props.onClick();
      await vue.nextTick();
    },
  };
}

test('pinned per-task failure keeps successful siblings and updates the rendered summary', async (t) => {
  const oracle = JSON.parse(
    readFileSync(
      new URL(
        '../../testdata/standard-module-host/transfer-task-loop-source-oracle.json',
        import.meta.url,
      ),
      'utf8',
    ),
  );
  const sample = oracle.cases.find((item) => item.name === 'subtitle_false');
  const items = sample.outcomes.map((success, index) => ({
    source: ['/owned/Owned.mkv', '/owned/Owned.srt', '/owned/Owned.flac'][index],
    target: success ? ['/library/Owned.mkv', '', '/library/Owned.flac'][index] : '',
    job_id: 'actual-group-job',
    status: success ? 'success' : 'failed',
    error: success ? '' : 'owned rejection',
  }));
  const dialog = openDialog(
    t,
    {},
    {
      api: async (path) => {
        if (path === '/files/organize')
          return {
            items: [{ source: items[0].source, job_id: 'actual-group-job', status: 'queued' }],
            total: 1,
            submitted: 1,
            failed: 0,
          };
        if (path === '/jobs/actual-group-job')
          return {
            job: { state: 'failed', error_message: 'owned rejection' },
            transfer: { items, total: 3, failed: sample.failed },
          };
        throw new Error('unexpected request ' + path);
      },
    },
  );
  await dialog.organize();
  const panel = dialog.nodes().find((node) => node.props?.class === 'organize-preview');
  const text = renderedText(panel).replace(/\s+/g, ' ');
  assert.match(text, /总数 3 · 失败 1/);
  assert.ok(text.includes('/library/Owned.mkv'));
  assert.ok(text.includes('/library/Owned.flac'));
  assert.equal(
    dialog
      .nodes()
      .filter(
        (node) =>
          node.props?.class === 'preview-failure' &&
          renderedText(node).trim() === 'owned rejection',
      ).length,
    1,
    'only the rejected subtitle has a row error',
  );
  assert.equal(dialog.requests.length, 2, 'one submission and actual job poll');
  assert.deepEqual(
    dialog.events,
    [['historyChanged']],
    'refresh history and keep the failed dialog open',
  );
});

test('queuing history closes the dialog as soon as background preparation is accepted', async (t) => {
  const dialog = openDialog(
    t,
    {},
    {
      historyIds: ['first-history', 'second-history'],
      api: async () => ({ preparation_job_id: 'history-preparation' }),
    },
  );
  await dialog.organize(true);
  assert.equal(dialog.requests.length, 1);
  assert.equal(dialog.requests[0].path, '/files/organize');
  assert.equal(dialog.requests[0].body.background, true);
  assert.deepEqual(dialog.requests[0].body.history_ids, ['first-history', 'second-history']);
  assert.deepEqual(dialog.events, [['done', 'history-preparation']]);
  assert.deepEqual(dialog.toasts, ['已提交 2 条整理记录，正在后台准备']);
  assert.ok(!dialog.nodes().some((node) => node.props?.class === 'organize-progress'));
});

test('manual target can be edited and cleared without changing storage, mode, or explicit switches', async (t) => {
  const dialog = openDialog(t);
  await dialog.select(0, 'local');
  await dialog.select(1, 'hardlink');
  await dialog.toggle(0, true);
  await dialog.toggle(1, true);
  await dialog.path('/library');
  await dialog.path('/library/custom');
  assert.equal(dialog.pathInput().props['model-value'], '/library/custom');
  assert.equal(dialog.pathInput().props.disabled, false);
  assert.equal(dialog.pathInput().props.readonly, undefined);
  assert.ok(dialog.switches().every((node) => node.props.checked));
  await dialog.path('');
  assert.equal(dialog.pathInput().props['model-value'], '');
  const body = await dialog.preview();
  assert.equal(body.target_path, '');
  assert.equal(body.target_storage, 'local');
  assert.equal(body.mode, 'hardlink');
  assert.equal(body.library_type_folder, true);
  assert.equal(body.scraping, true);
});

test('storage and method changes preserve the custom target and explicit classification setting', async (t) => {
  const dialog = openDialog(t, { target_path: '/library/original' });
  await dialog.path('/library/custom');
  await dialog.toggle(0, true);
  await dialog.select(0, '');
  await dialog.select(1, 'copy');
  await dialog.select(1, 'hardlink');
  assert.equal(dialog.pathInput().props['model-value'], '/library/custom');
  const body = await dialog.preview();
  assert.equal(body.target_path, '/library/custom');
  assert.equal(body.target_storage, '');
  assert.equal(body.mode, 'hardlink');
  assert.equal(body.library_type_folder, true);
});

test('choosing automatic target retains other automatic values and preserves explicit false switches', async (t) => {
  const dialog = openDialog(t);
  await dialog.path('/library');
  await dialog.toggle(0, false);
  await dialog.toggle(1, false);
  await dialog.path('自动');
  assert.equal(dialog.pathInput().props['model-value'], '');
  const body = await dialog.preview();
  assert.equal(body.target_path, '');
  assert.equal(body.target_storage, '');
  assert.equal(body.mode, '');
  assert.equal(body.library_type_folder, false);
  assert.equal(body.scraping, false);
});

test('directory tree selection only changes the target path', async (t) => {
  const dialog = openDialog(t);
  await dialog.select(0, 'local');
  await dialog.select(1, 'hardlink');
  await dialog.toggle(0, true);
  await dialog.toggle(1, true);
  await dialog.browsePath('/library/from-tree');
  assert.equal(dialog.pathInput().props['model-value'], '/library/from-tree');
  const body = await dialog.preview();
  assert.equal(body.target_path, '/library/from-tree');
  assert.equal(body.target_storage, 'local');
  assert.equal(body.mode, 'hardlink');
  assert.equal(body.library_type_folder, true);
  assert.equal(body.scraping, true);
});

function renderedText(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(renderedText).join(' ');
  return renderedText(node.children);
}

test('preview remains an action and every later click requests current form values again', async (t) => {
  const dialog = openDialog(
    t,
    {},
    {
      api: async (_path, body, count) => ({
        items: [
          {
            source: '/downloads/Test.mkv',
            target: `${body.target_path}/preview-${count}.mkv`,
            status: 'ready',
          },
        ],
        total: 1,
        submitted: 0,
        failed: 0,
      }),
    },
  );
  await dialog.path('/library/first');
  await dialog.select(1, 'hardlink');
  await dialog.preview();
  assert.equal(dialog.requests.length, 1);
  assert.equal(renderedText(dialog.previewButton()).trim(), '预览');
  assert.ok(dialog.nodes().some((node) => node.props?.class === 'organize-preview'));
  assert.ok(
    dialog.nodes().some((node) => renderedText(node).includes('/library/first/preview-1.mkv')),
  );

  await dialog.preview();
  assert.equal(dialog.requests.length, 2, 'the second click must request again instead of hiding');
  assert.equal(renderedText(dialog.previewButton()).trim(), '预览');
  assert.ok(dialog.nodes().some((node) => node.props?.class === 'organize-preview'));
  assert.ok(
    dialog.nodes().some((node) => renderedText(node).includes('/library/first/preview-2.mkv')),
  );

  await dialog.path('/library/changed');
  await dialog.select(1, 'copy');
  await dialog.toggle(0, true);
  await dialog.preview();
  assert.equal(dialog.requests.length, 3);
  assert.deepEqual(
    dialog.requests.map(({ body }) => [body.target_path, body.mode, body.preview, body.background]),
    [
      ['/library/first', 'hardlink', true, false],
      ['/library/first', 'hardlink', true, false],
      ['/library/changed', 'copy', true, false],
    ],
  );
  assert.equal(dialog.requests[2].body.library_type_folder, true);
  assert.ok(
    dialog.nodes().some((node) => renderedText(node).includes('/library/changed/preview-3.mkv')),
  );
  assert.equal(dialog.pathInput().props['model-value'], '/library/changed');
});

test('a pending preview keeps its label and blocks duplicate requests', async (t) => {
  let resolve;
  const dialog = openDialog(
    t,
    {},
    {
      api: () =>
        new Promise((finish) => {
          resolve = finish;
        }),
    },
  );
  const pending = dialog.preview();
  await vue.nextTick();
  assert.equal(dialog.requests.length, 1);
  assert.equal(renderedText(dialog.previewButton()).trim(), '预览');
  assert.equal(dialog.previewButton().props.disabled, true);
  assert.equal(dialog.previewButton().props['aria-busy'], true);
  await dialog.preview();
  assert.equal(dialog.requests.length, 1);
  resolve({ items: [], total: 0, submitted: 0, failed: 0 });
  await pending;
  assert.equal(dialog.previewButton().props.disabled, false);
  assert.equal(dialog.previewButton().props['aria-busy'], false);
});

test('failed refresh clears old preview results, retains the form, and allows another preview', async (t) => {
  const dialog = openDialog(
    t,
    { target_path: '/library/retained' },
    {
      api: async (_path, _body, count) => {
        if (count === 2) throw new Error('无法读取源文件，请重试');
        return {
          items: [
            {
              source: '/downloads/Test.mkv',
              target: `/library/retained/result-${count}.mkv`,
              status: 'ready',
            },
          ],
          total: 1,
          submitted: 0,
          failed: 0,
        };
      },
    },
  );
  await dialog.preview();
  await dialog.preview();
  assert.equal(dialog.requests.length, 2);
  assert.ok(dialog.nodes().some((node) => renderedText(node).includes('无法读取源文件，请重试')));
  assert.equal(
    dialog.nodes().some((node) => renderedText(node).includes('result-1.mkv')),
    false,
  );
  assert.equal(dialog.pathInput().props['model-value'], '/library/retained');
  assert.equal(renderedText(dialog.previewButton()).trim(), '预览');
  assert.equal(dialog.previewButton().props.disabled, false);
  await dialog.preview();
  assert.equal(dialog.requests.length, 3);
  assert.ok(dialog.nodes().some((node) => renderedText(node).includes('result-3.mkv')));
});

test('settings read after early history render stays local and cannot repopulate a logged-out session', async (t) => {
  let resolve;
  const pending = new Promise((done) => {
    resolve = done;
  });
  const dialog = openDialog(t, {}, { noSettings: true, api: () => pending });
  const mounted = dialog.mount();
  assert.equal(dialog.requests[0].path, '/settings');
  assert.equal(dialog.requests[0].signal.aborted, false);
  dialog.unmount();
  assert.equal(dialog.requests[0].signal.aborted, true);
  resolve({ directories: [{ library_path: '/old-session' }] });
  await mounted;
  assert.equal(dialog.ui.settings, null, 'late response must not populate global settings');
  assert.equal(
    dialog.nodes().some((node) => node.type === 'option' && node.props?.value === '/old-session'),
    false,
  );
});

test('early history dialog can use local settings without replacing shared configuration', async (t) => {
  const dialog = openDialog(
    t,
    {},
    { noSettings: true, api: async () => ({ directories: [{ library_path: '/local-choice' }] }) },
  );
  await dialog.mount();
  await vue.nextTick();
  assert.equal(dialog.ui.settings, null);
  assert.ok(
    dialog.nodes().some((node) => node.type === 'option' && node.props?.value === '/local-choice'),
  );
  dialog.unmount();
});
