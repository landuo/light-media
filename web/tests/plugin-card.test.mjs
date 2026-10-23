import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

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
