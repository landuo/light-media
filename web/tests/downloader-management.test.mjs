import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const copy = (value) => JSON.parse(JSON.stringify(value));
const { descriptor } = parse(
  readFileSync(new URL('../src/components/DownloaderManager.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'DownloaderManager', inlineTemplate: true });
const source = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function nodeText(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(nodeText).join(' ');
  return nodeText(node.children);
}

async function mount(t, initial = {}) {
  let settings = {
    downloaders: [],
    default_downloader: '',
    tmdb: { api_key: '', access_token: '' },
    allowed_roots: ['/downloads', '/media'],
    directories: [{ id: 'existing-rule', source_path: '/downloads', library_path: '/media' }],
    secret_state: { downloaders: { existing: { password: true } } },
    ...copy(initial),
  };
  const fixture = { requests: [], toasts: [], randomCalls: 0 },
    mounted = [];
  // LAN HTTP exposes getRandomValues but does not expose secure-context randomUUID.
  const lanCrypto = {
    getRandomValues(array) {
      fixture.randomCalls++;
      return webcrypto.getRandomValues(array);
    },
  };
  assert.equal(lanCrypto.randomUUID, undefined);
  const managed = () => ({
    downloaders: settings.downloaders.map((config) => ({
      config: copy(config),
      default: config.id === settings.default_downloader,
      credential_configured: config.id === 'existing',
      connection: {},
    })),
    default_downloader: settings.default_downloader,
  });
  const api = async (path, body, method) => {
    fixture.requests.push({ path, body: body === undefined ? undefined : copy(body), method });
    if (path === '/downloaders/manage') return managed();
    if (path === '/settings' && method === 'PUT') {
      settings = copy(body);
      return copy(settings);
    }
    throw new Error('Unexpected request ' + path);
  };
  const module = { exports: {} };
  new Function('require', 'exports', 'crypto', source)(
    (dependency) => {
      if (dependency === 'vue')
        return {
          ...vue,
          onMounted: (callback) => mounted.push(callback),
          defineAsyncComponent: () => ({ name: 'DownloaderConfigDialog' }),
          withDirectives: (node) => node,
        };
      if (dependency === '../api')
        return {
          api,
          action: async (callback) => callback(),
          clone: copy,
          errorText: (error) => error.message,
          friendly: String,
          refreshSettings: async () => copy(settings),
          toast: (message) => fixture.toasts.push(message),
        };
      if (dependency.endsWith('.vue')) return { name: 'AppIcon' };
      throw new Error('Unexpected dependency ' + dependency);
    },
    module.exports,
    lanCrypto,
  );
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  t.after(() => scope.stop());
  for (const callback of mounted) await callback();
  await vue.nextTick();

  function nodes() {
    const list = [];
    function visit(node) {
      if (node == null) return;
      if (Array.isArray(node)) return node.forEach(visit);
      list.push(node);
      if (typeof node === 'object') visit(node.children);
    }
    visit(render({}, []));
    return list;
  }
  const cards = () =>
    nodes().filter(
      (node) => node.type === 'article' && node.props?.class?.includes('downloader-card'),
    );
  const button = (name) =>
    nodes().find(
      (node) =>
        node.type === 'button' &&
        (node.props?.['aria-label'] === name || nodeText(node).trim() === name),
    );
  return {
    fixture,
    nodes,
    cards,
    button,
    async click(name) {
      const node = button(name);
      assert.ok(node, name);
      await node.props.onClick({ stopPropagation() {}, preventDefault() {} });
      await vue.nextTick();
    },
    async add(kind) {
      await this.click('添加下载器');
      assert.equal(button('添加下载器').props['aria-expanded'], true);
      assert.ok(button('qBittorrent'));
      assert.ok(button('Transmission'));
      await this.click(kind);
      assert.equal(button('添加下载器').props['aria-expanded'], false);
    },
  };
}

test('LAN HTTP can add both downloader types as distinct disabled drafts and edit their cards', async (t) => {
  const app = await mount(t);
  assert.equal(app.cards().length, 0);

  await app.add('qBittorrent');
  await app.add('Transmission');
  const cards = app.cards();
  assert.equal(cards.length, 2);
  assert.match(nodeText(cards[0]), /下载器1.*qBittorrent/);
  assert.match(nodeText(cards[1]), /下载器2.*Transmission/);
  assert.ok(cards.every((card) => card.props.class.includes('disabled')));
  assert.ok(cards.every((card) => String(card.key).startsWith('downloader-')));
  assert.notEqual(cards[0].key, cards[1].key);
  assert.equal(app.fixture.randomCalls, 2);
  assert.deepEqual(
    app.fixture.requests.map((request) => request.path),
    ['/downloaders/manage'],
    'adding a draft must not persist settings or contact a downloader',
  );

  const editor = () => app.nodes().find((node) => node.type?.name === 'DownloaderConfigDialog');
  for (const card of cards) {
    await card.props.onClick();
    await vue.nextTick();
    const dialog = editor();
    assert.ok(dialog);
    assert.equal(dialog.props.value.name, nodeText(card).match(/下载器\d+/)[0]);
    await dialog.props.onClose();
    await vue.nextTick();
    assert.equal(editor(), undefined);
  }

  await app.cards()[0].props.onClick();
  await vue.nextTick();
  // The Vuetify child edits an isolated draft. Close discards changes; its
  // validated confirm event updates this card without submitting settings.
  editor().props.value.name = 'discarded draft';
  await editor().props.onClose();
  await vue.nextTick();
  assert.match(nodeText(app.cards()[0]), /下载器1/);
  await app.cards()[0].props.onClick();
  await vue.nextTick();
  editor().props.value.name = '家庭 qB';
  await editor().props.onConfirm();
  await vue.nextTick();
  assert.match(nodeText(app.cards()[0]), /家庭 qB/);
  assert.equal(app.cards()[0].key, cards[0].key);
  assert.ok(app.cards()[0].props.class.includes('disabled'));
  assert.equal(editor(), undefined);
});

test('section save preserves existing downloader identity and unrelated settings while saving new disabled drafts', async (t) => {
  const existing = {
    id: 'existing',
    name: '下载器2',
    kind: 'qbittorrent',
    base_url: 'http://downloader.invalid:8080',
    username: 'synthetic-user',
    password: '',
    enabled: true,
  };
  const app = await mount(t, { downloaders: [existing], default_downloader: existing.id });
  await app.add('qBittorrent');
  await app.add('Transmission');
  await app.click('保存');

  const saves = app.fixture.requests.filter(
    (request) => request.path === '/settings' && request.method === 'PUT',
  );
  assert.equal(saves.length, 1);
  const saved = saves[0].body;
  assert.deepEqual(saved.downloaders[0], existing);
  assert.equal(saved.default_downloader, existing.id);
  assert.deepEqual(
    saved.downloaders.slice(1).map((row) => row.name),
    ['下载器3', '下载器4'],
  );
  assert.deepEqual(
    saved.downloaders.slice(1).map((row) => row.kind),
    ['qbittorrent', 'transmission'],
  );
  assert.ok(
    saved.downloaders.slice(1).every((row) => row.enabled === false && row.password === ''),
  );
  assert.equal(new Set(saved.downloaders.map((row) => row.id)).size, 3);
  assert.deepEqual(saved.allowed_roots, ['/downloads', '/media']);
  assert.deepEqual(saved.directories, [
    { id: 'existing-rule', source_path: '/downloads', library_path: '/media' },
  ]);
  assert.deepEqual(saved.tmdb, { api_key: '', access_token: '' });
  assert.equal('secret_state' in saved, false);
  assert.equal(app.cards().length, 3);
});
