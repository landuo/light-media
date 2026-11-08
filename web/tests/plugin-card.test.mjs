import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as cloneHelpers from '../src/features/plugin-clone.ts';
import * as forms from '../src/features/plugin-forms.ts';

const colors = JSON.parse(
  readFileSync(new URL('../src/features/plugin-icon-colors.json', import.meta.url)),
);
const { descriptor } = parse(
  readFileSync(new URL('../src/components/PluginManageCard.vue', import.meta.url), 'utf8'),
);
const compiled = ts.transpileModule(
  compileScript(descriptor, { id: 'PluginManageCard', inlineTemplate: true }).content,
  {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  },
).outputText;
function mount(t, icon) {
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue') return vue;
    if (name === '../features/plugin-clone') return cloneHelpers;
    if (name === '../features/plugin-forms') return forms;
    if (name.endsWith('plugin-icon-colors.json')) return colors;
    if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
    throw new Error('Unexpected dependency ' + name);
  }, module.exports);
  const props = vue.reactive({
    plugin: { icon, name: 'Fixture', version: '1.0', author: 'Fixture', installed: true },
  });
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup(props, { expose() {}, emit() {} }));
  t.after(() => scope.stop());
  function nodes() {
    const result = [];
    function visit(node) {
      if (Array.isArray(node)) return node.forEach(visit);
      if (!node || typeof node !== 'object') return;
      result.push(node);
      if (node.children?.default) visit(node.children.default({ props: {} }));
      else visit(node.children);
    }
    visit(render({}, []));
    return result;
  }
  return {
    props,
    nodes,
    image: () => nodes().find((node) => node.type === 'img'),
    cover: () => nodes().find((node) => node.props?.class === 'plugin-card-cover'),
  };
}

test('original plugin icons stay local and use their original cover colors; absent icons use the original default logo', (t) => {
  const known = Object.keys(colors).find((key) => key !== 'plugin.png');
  const app = mount(t, 'https://raw.githubusercontent.com/example/icons/' + known);
  assert.equal(app.image().props.src, '/plugin_icon/' + known);
  assert.ok(app.cover().props.style.background.endsWith(colors[known]));
  for (const icon of ['uninstall.png', '../not-bundled.png', 'data:image/png;base64,unknown', '']) {
    app.props.plugin.icon = icon;
    assert.equal(app.image().props.src, '/plugin_icon/plugin.png');
    assert.ok(app.cover().props.style.background.endsWith(colors['plugin.png']));
  }
});

test('custom clone icons keep their safe URL and reset a failed image when its full URL changes', async (t) => {
  const app = mount(t, 'https://synthetic.invalid/first/custom.svg');
  assert.equal(app.image().props.src, 'https://synthetic.invalid/first/custom.svg');
  assert.equal(app.image().props.referrerpolicy, 'no-referrer');
  app.image().props.onError();
  assert.equal(app.image().props.src, '/plugin_icon/plugin.png');
  app.props.plugin.icon = 'https://synthetic.invalid/second/custom.svg';
  await vue.nextTick();
  assert.equal(app.image().props.src, 'https://synthetic.invalid/second/custom.svg');
  for (const icon of [
    'javascript:alert(1)',
    '//synthetic.invalid/custom.svg',
    'https://user:secret@synthetic.invalid/custom.svg',
  ]) {
    app.props.plugin.icon = icon;
    await vue.nextTick();
    assert.equal(app.image().props.src, '/plugin_icon/plugin.png');
  }
});

test('restyled cards show the clone display version and mount bundled icons under the application path', (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'location');
  Object.defineProperty(globalThis, 'location', {
    value: { pathname: '/light-media/' },
    configurable: true,
  });
  t.after(() =>
    previous ? Object.defineProperty(globalThis, 'location', previous) : delete globalThis.location,
  );
  const app = mount(t, 'plugin.png');
  app.props.plugin.display_version = 'view-9';
  assert.equal(app.image().props.src, '/light-media/plugin_icon/plugin.png');
  assert.equal(
    app.nodes().find((node) => node.props?.class === 'plugin-version').children,
    'vview-9',
  );
  assert.equal(app.props.plugin.version, '1.0');
});

test('a failed icon switches both image and cover color to the original fallback, then resets when the plugin changes', async (t) => {
  const known = Object.keys(colors).filter((key) => key !== 'plugin.png');
  const app = mount(t, known[0]);
  app.image().props.onError();
  assert.equal(app.image().props.src, '/plugin_icon/plugin.png');
  assert.ok(app.cover().props.style.background.endsWith(colors['plugin.png']));
  app.props.plugin.icon = known[1];
  await vue.nextTick();
  assert.equal(app.image().props.src, '/plugin_icon/' + known[1]);
  assert.ok(app.cover().props.style.background.endsWith(colors[known[1]]));
});

test('actual card keeps source label chips and hides installation statistics', (t) => {
  const app = mount(t, 'plugin.png');
  const counter = () => app.nodes().find(node => node.props?.class === 'plugin-download-count');
  assert.equal(counter(), undefined);
  app.props.count = 12345;
  assert.equal(counter(), undefined);
  app.props.plugin.plugin_label = ' tools, media , ';
  const chips = app.nodes().filter(node => node.type?.name === 'VChip');
  assert.deepEqual(chips.map(node => node.children.default()[0].children), ['tools', 'media']);
});

test('actual author links allow only HTTP(S) without credentials and retain invalid authors as text', (t) => {
  const app = mount(t, 'plugin.png');
  const link = () => app.nodes().find(node => node.props?.class === 'plugin-author-name' && node.type === 'a');
  const text = () => app.nodes().find(node => node.props?.class === 'plugin-author-name' && node.type === 'span');
  for (const url of ['https://synthetic.invalid/author', 'http://synthetic.invalid/author']) {
    app.props.plugin.author_url = url;
    assert.equal(link().props.href, url);
    assert.equal(link().props.target, '_blank');
    assert.equal(link().props.rel, 'noopener noreferrer');
    assert.equal(link().children, 'Fixture');
  }
  for (const url of [
    'javascript:alert(1)',
    'data:text/html,unsafe',
    '//synthetic.invalid/author',
    '/author',
    'https://user:secret@synthetic.invalid/author',
    'http://user@synthetic.invalid/author',
    'https://:secret@synthetic.invalid/author',
    'https://',
    '',
    undefined,
  ]) {
    app.props.plugin.author_url = url;
    assert.equal(link(), undefined, String(url));
    assert.equal(text().children, 'Fixture');
  }
  app.props.plugin.author_url = 'https://synthetic.invalid/author';
  assert.equal(link().props.href, app.props.plugin.author_url);
  app.props.sortable = true;
  assert.equal(link(), undefined);
  assert.equal(text().children, 'Fixture');
});

test('actual cards retain each author name and link only the declared matching group member', (t) => {
  const app = mount(t, 'plugin.png');
  const names = () => app.nodes().filter(node => node.props?.class === 'plugin-author-name')
    .map(node => [node.children, node.props.href]);
  app.props.plugin.author = 'jxxghp,landuo';
  app.props.plugin.author_url = 'https://github.com/landuo';
  assert.deepEqual(names(), [['jxxghp', undefined], ['landuo', 'https://github.com/landuo']]);

  app.props.plugin.author = 'jxxghp, Ckun';
  app.props.plugin.author_url = 'https://github.com/CKun/';
  assert.deepEqual(names(), [['jxxghp', undefined], ['Ckun', 'https://github.com/CKun/']]);

  for (const url of ['https://example.invalid/CKun', 'https://github.com/other', 'https://github.com/CKun/repo']) {
    app.props.plugin.author_url = url;
    assert.deepEqual(names(), [['jxxghp', undefined], ['Ckun', undefined]]);
  }
});
