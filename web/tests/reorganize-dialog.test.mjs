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
      requests.push({ path, body, method, ...(signal ? { signal } : {}) });
      if (options.api) return options.api(path, body, requests.length);
      return { items: [], total: 0, submitted: 0, failed: 0 };
    },
    organizeStream: async (body, progress, signal) => {
      requests.push({ path: '/files/organize', body });
      return options.stream(body, progress, signal);
    },
    label: (value) => value,
    errorText: (error) => error.message,
    image: (path) => path,
    toast: (message) => toasts.push(message),
  };
  if (options.recognitionSource)
    apiModule.ui.settings.effective_recognition_source = options.recognitionSource;
  if (options.noSettings) apiModule.ui.settings = null;
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api') return apiModule;
    if (name === '../features/bounded-read') return { boundedRead: (read, signal) => read(signal) };
    if (name === './FileManagerIcon.vue') return { default: {} };
    if (name === './DirectoryPathPicker.vue') return { name: 'DirectoryPathPicker' };
    if (/^vuetify\/components\/(VTextField|VSelect|VTextarea|VBtn|VTooltip)$/.test(name)) {
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
      {
        paths: options.paths || ['/downloads/Test.mkv'],
        items: options.items,
        historyIds: options.historyIds,
        initial,
      },
      { expose: () => {}, emit: (...event) => events.push(event) },
    ),
  );
  t.after(() => {
    unmounted();
    scope.stop();
  });
  function nodes() {
    const result = [];
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) return node.forEach(visit);
      result.push(node);
      if (node.children && !Array.isArray(node.children) && typeof node.children === 'object') {
        for (const slot of Object.values(node.children))
          if (typeof slot === 'function') visit(slot({ props: {} }));
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

test('global Douban uses source ID without any TMDB episode group', async (t) => {
  const dialog = openDialog(t, { media_type: 'tv' }, { recognitionSource: 'douban' });
  const field = dialog.nodes().find((n) => n.props?.label === '豆瓣编号');
  assert.ok(field);
  field.props['onUpdate:modelValue']('701');
  await vue.nextTick();
  assert.ok(!dialog.nodes().some((n) => n.props?.label === '剧集组'));
  const body = await dialog.preview();
  assert.equal(body.tmdb_id, 0);
  assert.equal(body.media_source, 'douban');
  assert.equal(body.media_id, '701');
  assert.equal(body.episode_group, undefined);
});

test('provider media picker queries the selected source and fills source_id rather than zero TMDB id', async (t) => {
  const dialog = openDialog(
    t,
    { media_type: 'movie' },
    {
      recognitionSource: 'douban',
      api: async (path) =>
        path.startsWith('/catalog/search?')
          ? {
              results: [
                {
                  id: 0,
                  source: 'douban',
                  source_id: '701',
                  title: '豆瓣条目',
                  media_type: 'movie',
                },
              ],
            }
          : { items: [], total: 0, submitted: 0, failed: 0 },
    },
  );
  dialog
    .nodes()
    .find((n) => n.props?.['aria-label'] === '查询媒体编号')
    .props.onClick();
  await vue.nextTick();
  dialog
    .nodes()
    .find((n) => n.props?.label === '搜索影视名称')
    .props['onUpdate:modelValue']('条目');
  await vue.nextTick();
  await dialog
    .nodes()
    .find((n) => n.type === 'form')
    .props.onSubmit({ preventDefault() {} });
  await vue.nextTick();
  const lookup = dialog.requests.find((r) => r.path.startsWith('/catalog/search?'));
  assert.equal(new URLSearchParams(lookup.path.split('?')[1]).get('source'), 'douban');
  dialog
    .nodes()
    .find((n) => n.type === 'button' && renderedText(n).includes('豆瓣条目'))
    .props.onClick();
  await vue.nextTick();
  await Promise.resolve();
  await vue.nextTick();
  assert.equal(dialog.nodes().find((n) => n.props?.label === '豆瓣编号').props.modelValue, '701');
  const body = await dialog.preview();
  assert.equal(body.media_id, '701');
  assert.equal(body.tmdb_id, 0);
});

test('history selections keep episode range editable without a filename template', async (t) => {
  const paths = ['/downloads/E01.mkv', '/downloads/E02.mkv'];
  const dialog = openDialog(t, { media_type: 'tv' }, { paths, historyIds: ['first', 'second'] });
  const range = dialog.nodes().find((node) => node.props?.label === '集');
  assert.equal(Boolean(range.props.disabled), false);
  range.props['onUpdate:modelValue']('3,4');
  await vue.nextTick();
  const request = await dialog.preview();
  assert.deepEqual(request.history_ids, ['first', 'second']);
  assert.equal(request.episode_detail, '3,4');
  assert.equal(request.episode_format, '');
});

test('multiple file and directory selections still require a template for episode range', async (t) => {
  for (const options of [
    { paths: ['/downloads/E01.mkv', '/downloads/E02.mkv'] },
    { paths: ['/downloads/Show'], items: [{ path: '/downloads/Show', type: 'dir' }] },
  ]) {
    const dialog = openDialog(t, { media_type: 'tv' }, options);
    const range = () => dialog.nodes().find((node) => node.props?.label === '集');
    assert.equal(Boolean(range().props.disabled), true);
    dialog
      .nodes()
      .find((node) => node.props?.label === '集数定位')
      .props['onUpdate:modelValue']('E{ep}');
    await vue.nextTick();
    assert.equal(Boolean(range().props.disabled), false);
  }
});

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
      stream: async (_, onProgress) => {
        onProgress({ stage: 'transferring', total: 1, completed: 0, current: items[0].source });
        return { items, total: 3, submitted: 1, failed: sample.failed };
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
  assert.equal(dialog.requests.length, 1, 'one foreground progress stream');
  assert.deepEqual(
    dialog.events,
    [['historyChanged']],
    'refresh history and keep the failed dialog open',
  );
});

test('history queue confirmation requires each selected job', async (t) => {
  for (const valid of [false, true]) {
    const items = ['first', 'second'].map((id) => ({ source: id, status: 'queued', job_id: id }));
    const dialog = openDialog(
      t,
      {},
      {
        historyIds: ['first', 'second'],
        api: async () =>
          valid
            ? { items, total: 2, submitted: 2, failed: 0 }
            : { preparation_job_id: 'preparing', items: [], total: 0, submitted: 0, failed: 0 },
      },
    );
    await dialog.organize(true);
    assert.deepEqual(dialog.requests[0].body.history_ids, ['first', 'second']);
    assert.deepEqual(dialog.toasts, valid ? ['已加入整理队列（2 项）'] : []);
    assert.deepEqual(dialog.events, valid ? [['done']] : []);
  }
});

test('immediate organize displays live progress and waits for the terminal result', async (t) => {
  let finish;
  const wait = new Promise((resolve) => {
    finish = resolve;
  });
  const dialog = openDialog(
    t,
    {},
    {
      stream: async (body, onProgress) => {
        assert.equal(body.background, false);
        onProgress({
          stage: 'transferring',
          current: '/src/Fallout.S02E02.mkv',
          total: 8,
          completed: 1,
          percent: 22,
        });
        await wait;
        return {
          items: [{ source: '/src/Fallout.S02E02.mkv', status: 'success', job_id: 'transfer' }],
          submitted: 1,
          total: 1,
          failed: 0,
        };
      },
    },
  );
  const run = dialog.organize();
  await vue.nextTick();
  assert.match(
    renderedText(dialog.nodes().find((n) => n.props?.class === 'organize-progress')),
    /Fallout.S02E02.mkv.*22%.*1 \/ 8/,
  );
  assert.deepEqual(dialog.toasts, []);
  assert.deepEqual(dialog.events, []);
  finish();
  await run;
  assert.deepEqual(dialog.toasts, ['整理完成']);
  assert.deepEqual(dialog.events, [['done']]);
  assert.equal(dialog.requests.length, 1);
});

test('a lost progress stream never reports success or replays submission', async (t) => {
  const dialog = openDialog(
    t,
    {},
    {
      stream: async (_, onProgress) => {
        onProgress({ stage: 'queued', total: 8, completed: 0 });
        throw new Error('连接中断');
      },
    },
  );
  await dialog.organize();
  assert.equal(dialog.requests.length, 1);
  assert.deepEqual(dialog.toasts, []);
  assert.deepEqual(dialog.events, [['historyChanged']]);
});
test('softlink is a real independent manual method and preserves target/switches', async (t) => {
  const dialog = openDialog(t);
  await dialog.select(0, 'local');
  await dialog.path('/library/linked');
  await dialog.toggle(0, true);
  await dialog.toggle(1, false);
  await dialog.select(1, 'softlink');
  const body = await dialog.preview();
  assert.equal(body.mode, 'softlink');
  assert.equal(body.target_path, '/library/linked');
  assert.equal(body.target_storage, 'local');
  assert.equal(body.library_type_folder, true);
  assert.equal(body.scraping, false);
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
  const settingsRequest = dialog.requests.find((request) => request.path === '/settings');
  assert.ok(settingsRequest);
  assert.equal(settingsRequest.signal.aborted, false);
  dialog.unmount();
  assert.equal(settingsRequest.signal.aborted, true);
  assert.ok(dialog.requests.every((request) => request.signal.aborted));
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

const recommended = {
  rule_name: '显式集号',
  pattern: '(?P<ep>\\d+)',
  episode_format: '{title}.E{ep}.{ext}',
  sample_file: 'Show.E01.mkv',
  sample_count: 3,
  matched_count: 2,
  message: '已根据配置规则生成并验证集数定位模板',
};
const recommendationButton = (dialog) =>
  dialog.nodes().find((node) => node.props?.['aria-label'] === '推荐集数定位');
const field = (dialog, name) => dialog.nodes().find((node) => node.props?.label === name);

test('recommendation calls the real route and changes only episode_format without confirmation', async (t) => {
  const dialog = openDialog(
    t,
    { media_type: 'tv', season: 2, tmdb_id: 123, target_path: '/library/custom' },
    {
      api: async (path) => (path.endsWith('/recommend') ? recommended : { items: [], total: 0 }),
    },
  );
  await dialog.select(1, 'copy');
  field(dialog, '集').props['onUpdate:modelValue']('4,6');
  field(dialog, '集数定位').props['onUpdate:modelValue']('old-{ep}');
  field(dialog, '集数偏移').props['onUpdate:modelValue']('-3');
  field(dialog, '指定Part').props['onUpdate:modelValue']('part2');
  field(dialog, '最小文件大小（MB）').props['onUpdate:modelValue'](42);
  await dialog.toggle(0, true);
  await dialog.toggle(1, true);
  const before = await dialog.preview();
  await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  await vue.nextTick();
  assert.deepEqual(dialog.requests.at(-1).body, { paths: ['/downloads/Test.mkv'] });
  assert.equal(dialog.requests.at(-1).path, '/files/episode-format/recommend');
  const after = await dialog.preview();
  assert.deepEqual(after, { ...before, episode_format: recommended.episode_format });
  assert.match(field(dialog, '集数定位').props.hint, /显式集号.*Show\.E01\.mkv.*2\/3/);
  assert.ok(dialog.toasts.includes('已覆盖集数定位，其他选项保持不变'));
  assert.equal(dialog.nodes().filter((node) => node.props?.role === 'dialog').length, 1);
});

test('history recommendation submits IDs only while explicit same-directory multi-select submits paths', async (t) => {
  for (const options of [
    { historyIds: ['history-one', 'history-two'], paths: ['/stale/display1', '/stale/display2'] },
    {
      paths: ['/downloads/Show.E01.mkv', '/downloads/Show.E02.mkv'],
      items: [
        { path: '/downloads/Show.E01.mkv', type: 'file' },
        { path: '/downloads/Show.E02.mkv', type: 'file' },
      ],
    },
  ]) {
    const dialog = openDialog(
      t,
      { media_type: 'tv' },
      { ...options, api: async () => recommended },
    );
    assert.equal(recommendationButton(dialog).props.disabled, false);
    await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
    assert.deepEqual(
      dialog.requests[0].body,
      options.historyIds ? { history_ids: options.historyIds } : { paths: options.paths },
    );
  }
});

test('invalid multi-selection disables recommendation, while an empty rule table keeps a clickable reminder', async (t) => {
  for (const options of [
    { paths: ['/one/E01.mkv', '/two/E02.mkv'] },
    {
      paths: ['/one', '/one/E02.mkv'],
      items: [
        { path: '/one', type: 'dir' },
        { path: '/one/E02.mkv', type: 'file' },
      ],
    },
  ]) {
    const dialog = openDialog(t, { media_type: 'tv' }, options);
    assert.equal(recommendationButton(dialog).props.disabled, true);
    await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
    assert.equal(dialog.requests.length, 0);
  }
  const dialog = openDialog(
    t,
    { media_type: 'tv' },
    { api: async () => ({ revision: 0, rules: [] }) },
  );
  await dialog.mount();
  await vue.nextTick();
  assert.equal(recommendationButton(dialog).props.disabled, false);
  const count = dialog.requests.length;
  await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  assert.equal(
    dialog.requests.length,
    count,
    'empty table reminder does not send a recommendation',
  );
  assert.ok(dialog.toasts.includes('手动整理集数定位规则为空，请先前往设置的规则页填写'));
  assert.ok(dialog.nodes().some((node) => renderedText(node).includes('手动整理集数定位规则为空')));
});

test('recommendation errors preserve existing format; pending responses never overwrite later user edits', async (t) => {
  const failed = openDialog(
    t,
    { media_type: 'tv' },
    {
      api: async () => {
        throw new Error('规则未通过附加文件兼容验证');
      },
    },
  );
  field(failed, '集数定位').props['onUpdate:modelValue']('keep-{ep}');
  await recommendationButton(failed).props.onClick({ stopPropagation() {} });
  assert.equal(field(failed, '集数定位').props.modelValue, 'keep-{ep}');
  assert.equal(field(failed, '集数定位').props.error, true);
  assert.match(field(failed, '集数定位').props.hint, /附加文件兼容/);
  let finish;
  const dialog = openDialog(
    t,
    { media_type: 'tv' },
    {
      api: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    },
  );
  const pending = recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  await vue.nextTick();
  assert.equal(recommendationButton(dialog).props.loading, true);
  assert.equal(dialog.previewButton().props.disabled, true);
  await dialog.preview();
  await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  assert.equal(dialog.requests.length, 1);
  field(dialog, '集数定位').props['onUpdate:modelValue']('edited-{ep}');
  finish(recommended);
  await pending;
  assert.equal(field(dialog, '集数定位').props.modelValue, 'edited-{ep}');
  assert.equal(dialog.toasts.length, 0);
});

const groupFixtures = [
  { id: 'group-a', name: '播出顺序', group_count: 3, episode_count: 24 },
  { id: 'group-b', name: 'DVD顺序', group_count: 2, episode_count: 24 },
];
const flushReads = async () => {
  await new Promise((resolve) => setImmediate(resolve));
  await vue.nextTick();
};

test('episode groups use the real catalog GET, default selection and independent manual payload', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const dialog = openDialog(
    t,
    {
      media_type: 'tv',
      tmdb_id: 123,
      episode_group: 'historical-group',
      target_path: '/library/custom',
    },
    {
      historyIds: ['history-one'],
      api: async (path) =>
        path.startsWith('/catalog/episode-groups?') ? groupFixtures : { items: [], total: 0 },
    },
  );
  assert.equal(
    field(dialog, '剧集组').props.modelValue,
    null,
    'no implicit inheritance from history defaults',
  );
  assert.equal(field(dialog, '剧集组').props.disabled, false);
  t.mock.timers.tick(399);
  assert.equal(dialog.requests.length, 0);
  t.mock.timers.tick(1);
  assert.equal(dialog.requests[0].path, '/catalog/episode-groups?id=123');
  assert.equal(dialog.requests[0].method, 'GET');
  await flushReads();
  const options = field(dialog, '剧集组').props.items;
  assert.deepEqual(options[0], {
    title: '不指定剧集组',
    subtitle: '使用 TMDB 默认季集排序',
    value: null,
  });
  assert.deepEqual(options[1], { title: '播出顺序', subtitle: '3 季 • 24 集', value: 'group-a' });
  assert.deepEqual(field(dialog, '剧集组').props['item-props'](options[1]), {
    title: '播出顺序',
    subtitle: '3 季 • 24 集',
  });
  await dialog.select(1, 'copy');
  await dialog.toggle(0, true);
  await dialog.toggle(1, true);
  const before = await dialog.preview();
  assert.equal(before.episode_group, null);
  assert.equal(before.from_history, false);
  field(dialog, '剧集组').props['onUpdate:modelValue']('group-a');
  await vue.nextTick();
  const after = await dialog.preview();
  assert.deepEqual(after, { ...before, episode_group: 'group-a' });
  field(dialog, '剧集组').props['onUpdate:modelValue'](null);
  await vue.nextTick();
  assert.deepEqual(await dialog.preview(), before);
});

test('changing TMDB identity clears episode groups and isolates a superseded response', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const finishes = new Map();
  const dialog = openDialog(
    t,
    { media_type: 'tv', tmdb_id: 123 },
    {
      api: (path) =>
        path.startsWith('/catalog/episode-groups?')
          ? new Promise((resolve) => finishes.set(path, resolve))
          : Promise.resolve({ items: [], total: 0 }),
    },
  );
  t.mock.timers.tick(400);
  assert.equal(field(dialog, '剧集组').props.loading, true);
  field(dialog, 'TheMovieDb编号').props['onUpdate:modelValue']('456');
  await vue.nextTick();
  assert.equal(dialog.requests[0].signal.aborted, true);
  assert.equal(field(dialog, '剧集组').props.modelValue, null);
  assert.equal(field(dialog, '剧集组').props.items.length, 1);
  t.mock.timers.tick(400);
  finishes.get('/catalog/episode-groups?id=123')([{ ...groupFixtures[0], id: 'stale-group' }]);
  await flushReads();
  assert.equal(field(dialog, '剧集组').props.items.length, 1);
  assert.equal(
    field(dialog, '剧集组').props.loading,
    true,
    'old finally must not clear new loading',
  );
  finishes.get('/catalog/episode-groups?id=456')(groupFixtures);
  await flushReads();
  assert.equal(field(dialog, '剧集组').props.loading, false);
  field(dialog, '剧集组').props['onUpdate:modelValue']('group-b');
  await vue.nextTick();
  for (const invalid of ['', '0', '-1', '1.5', 'not-a-number']) {
    field(dialog, 'TheMovieDb编号').props['onUpdate:modelValue'](invalid);
    await vue.nextTick();
    t.mock.timers.tick(400);
    assert.equal(field(dialog, '剧集组').props.disabled, true);
    assert.equal(field(dialog, '剧集组').props.modelValue, null);
  }
  assert.equal(
    dialog.requests.filter((r) => r.path.startsWith('/catalog/episode-groups?')).length,
    2,
  );
  await dialog.select(2, 'movie');
  assert.equal(field(dialog, '剧集组'), undefined);
  assert.equal((await dialog.preview()).episode_group, undefined);
  await dialog.select(2, 'tv');
  assert.equal(field(dialog, '剧集组').props.disabled, true);
  assert.equal(field(dialog, '剧集组').props.modelValue, null);
});

test('episode group lookup errors retain the form and unmount aborts the active read', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let finish;
  const dialog = openDialog(
    t,
    { media_type: 'tv', tmdb_id: 123, target_path: '/library/kept' },
    {
      api: async (path) => {
        if (path.endsWith('id=123')) throw new Error('剧集组查询失败');
        if (path.endsWith('id=456'))
          return new Promise((resolve) => {
            finish = resolve;
          });
        return { items: [], total: 0 };
      },
    },
  );
  t.mock.timers.tick(400);
  await flushReads();
  assert.equal(field(dialog, '剧集组').props.loading, false);
  assert.equal(field(dialog, '剧集组').props.error, true);
  assert.match(field(dialog, '剧集组').props.hint, /剧集组查询失败/);
  assert.equal(dialog.pathInput().props['model-value'], '/library/kept');
  assert.equal((await dialog.preview()).episode_group, null);
  field(dialog, 'TheMovieDb编号').props['onUpdate:modelValue']('456');
  await vue.nextTick();
  t.mock.timers.tick(400);
  const request = dialog.requests.at(-1);
  dialog.unmount();
  assert.equal(request.signal.aborted, true);
  finish(groupFixtures);
  await flushReads();
  assert.equal(field(dialog, '剧集组').props.items.length, 1);
});

test('a nonempty disabled rule table permits automatic fallback and keeps all other drafts', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let failRecommendation = false;
  const automatic = {
    ...recommended,
    rule_name: '智能分析',
    pattern: '',
    episode_format: 'Show.E{ep}.{a}',
    majority_count: 2,
    confidence: 'low',
    size_filter_relaxed: true,
    native_verified_count: 1,
    native_fallback_count: 1,
    reason: 'small_files_fallback',
    reasons: ['small_files_fallback', 'native_meta_fallback'],
    message: '已放宽体积限制智能生成模板（仅供参考）',
  };
  const dialog = openDialog(
    t,
    { media_type: 'tv', tmdb_id: 123, target_path: '/library/kept' },
    {
      api: async (path) => {
        if (path.endsWith('/rules')) return { rules: [{ enabled: false }] };
        if (path.startsWith('/catalog/episode-groups?')) return groupFixtures;
        if (path.endsWith('/recommend')) {
          if (failRecommendation)
            throw new Error('样本命名与原生识别结果冲突，建议补充集数定位规则');
          return automatic;
        }
        return { items: [], total: 0 };
      },
    },
  );
  await dialog.mount();
  t.mock.timers.tick(400);
  await flushReads();
  field(dialog, '剧集组').props['onUpdate:modelValue']('group-b');
  await dialog.select(1, 'hardlink');
  await dialog.toggle(0, true);
  field(dialog, '集数偏移').props['onUpdate:modelValue']('-2');
  await vue.nextTick();
  const before = await dialog.preview();
  assert.equal(recommendationButton(dialog).props.disabled, false);
  await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  assert.deepEqual(await dialog.preview(), { ...before, episode_format: automatic.episode_format });
  const hint = field(dialog, '集数定位').props.hint;
  for (const value of [
    '智能分析',
    'Show.E01.mkv',
    '置信度：低',
    '多数派 2/3',
    '原生验证 1 项',
    '原生回退 1 项',
    automatic.message,
  ])
    assert.ok(hint.includes(value), value);
  failRecommendation = true;
  field(dialog, '集数定位').props['onUpdate:modelValue']('manual-{ep}');
  await recommendationButton(dialog).props.onClick({ stopPropagation() {} });
  assert.equal(field(dialog, '集数定位').props.modelValue, 'manual-{ep}');
  assert.match(field(dialog, '集数定位').props.hint, /原生识别结果冲突/);
  assert.equal(field(dialog, '剧集组').props.modelValue, 'group-b');
  assert.equal(dialog.pathInput().props['model-value'], '/library/kept');
});
