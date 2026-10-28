import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as modes from '../src/features/interface-mode.ts';
import * as navigation from '../src/features/plugin-navigation.ts';

function mount(t, filename, props = {}, extras = {}) {
  const { descriptor } = parse(
    readFileSync(new URL('../src/' + filename, import.meta.url), 'utf8'),
  );
  const script = compileScript(descriptor, { id: filename });
  const template = compileTemplate({
    source: descriptor.template.content,
    filename,
    id: filename,
    compilerOptions: { bindingMetadata: script.bindings },
  });
  assert.deepEqual(template.errors, []);
  const transpile = (source) =>
    ts.transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText;
  const module = { exports: {} },
    rendered = { exports: {} },
    scope = vue.effectScope();
  const events = [];
  const ui = vue.reactive({ settings: {}, status: {}, busy: 0, toasts: [] });
  const require = (name) => {
    if (name === 'vue')
      return {
        ...vue,
        onMounted() {},
        onUnmounted() {},
        defineAsyncComponent: (loader) => ({
          name: String(loader).match(/([A-Z]\w*)\.vue/)?.[1] || 'Async',
        }),
      };
    if (name.endsWith('/interface-mode')) return modes;
    if (name.endsWith('/plugin-navigation')) return navigation;
    if (name.endsWith('/plugin-remote-api')) return { abortPluginAPIRequests: () => extras.abort?.() };
    if (name.endsWith('/api'))
      return { ui, api: async (path) => extras.api?.(path) ?? [], errorText: (e) => e.message };
    if (name.endsWith('/system-messages'))
      return { createSystemMessageFeed: () => ({ destroy() {}, setAuthenticated() {} }) };
    if (name.endsWith('/search-session'))
      return { searchSession: { lastRoute: 'search?query=retained' } };
    if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, name) => ({ name }) });
    if (name.endsWith('.vue')) return { default: { name: name.split('/').at(-1).slice(0, -4) } };
    throw new Error('Unexpected import: ' + name);
  };
  new Function('require', 'exports', 'location', transpile(script.content))(
    require,
    module.exports,
    { hash: '#apps' },
  );
  new Function('require', 'exports', transpile(template.code))(require, rendered.exports);
  const state = scope.run(() =>
    module.exports.default.setup(props, { expose() {}, emit: (...args) => events.push(args) }),
  );
  t.after(() => scope.stop());
  function nodes() {
    const result = [];
    function visit(node) {
      if (Array.isArray(node)) return node.forEach(visit);
      if (!node || typeof node !== 'object') return;
      result.push(node);
      if (node.children && !Array.isArray(node.children) && typeof node.children === 'object') {
        for (const [key, slot] of Object.entries(node.children))
          if (key !== '_' && typeof slot === 'function') visit(slot({ props: {} }));
      } else visit(node.children);
    }
    visit(rendered.exports.render(vue.proxyRefs(state), [], props, vue.proxyRefs(state)));
    return result;
  }
  return { state, nodes, events };
}

test('original auto detection and explicit app/desktop choices persist across reloads', (t) => {
  const previous = modes.interfaceMode.value;
  t.after(() => modes.setInterfaceMode(previous, { setItem() {} }));
  const data = new Map(),
    storage = { getItem: (key) => data.get(key), setItem: (key, value) => data.set(key, value) };
  for (const choice of ['auto', 'desktop', 'app']) {
    modes.setInterfaceMode(choice, storage);
    assert.equal(modes.readInterfaceMode(storage), choice);
    assert.equal(modes.interfaceMode.value, choice);
  }
  assert.equal(modes.isAppInterface('app', false, 1600), true);
  assert.equal(modes.isAppInterface('desktop', true, 390), false);
  assert.equal(modes.isAppInterface('auto', true, 1279), true);
  assert.equal(modes.isAppInterface('auto', true, 1280), false);
  assert.equal(modes.isAppInterface('auto', false, 900), false);
  for (const environment of [
    { standalone: true },
    { serviceWorker: true },
    { touch: true },
    { userAgent: 'iPhone' },
    { referrer: 'android-app://owned' },
    { width: 600 },
  ])
    assert.equal(modes.isPluginPWAEnvironment({ width: 1000, ...environment }), true);
  assert.equal(modes.isPluginPWAEnvironment({ width: 1000 }), false);
  assert.equal(modes.readInterfaceMode({ getItem: () => 'invalid' }), 'auto');
});

test('actual avatar layout menu changes the shared mode and writes only the layout preference', (t) => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage'),
    saved = new Map();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { setItem: (key, value) => saved.set(key, value) },
  });
  const previous = modes.interfaceMode.value;
  t.after(() => {
    modes.setInterfaceMode(previous, { setItem() {} });
    descriptor
      ? Object.defineProperty(globalThis, 'localStorage', descriptor)
      : delete globalThis.localStorage;
  });
  const f = mount(t, 'components/InterfaceModeMenu.vue');
  assert.ok(f.nodes().some((node) => node.props?.['aria-label'] === '用户界面设置'));
  const choices = f
    .nodes()
    .filter((node) => node.type?.name === 'VListItem' && node.props?.onClick);
  assert.equal(choices.length, 3);
  for (const [index, choice] of ['auto', 'desktop', 'app'].entries()) {
    choices[index].props.onClick();
    assert.equal(modes.interfaceMode.value, choice);
    assert.equal(saved.get(modes.interfaceModeKey), choice);
  }
  assert.equal(saved.size, 1);
});

test('actual App footer More opens apps and the application center shares the authenticated navigation cache', async (t) => {
  const previous = modes.interfaceMode.value;
  modes.setInterfaceMode('app', { setItem() {} });
  t.after(() => modes.setInterfaceMode(previous, { setItem() {} }));
  let calls = 0;
  const plugin = {
    plugin_id: 'Demo',
    instance_id: 'Demo_clone',
    nav_key: 'tasks',
    title: 'Plugin tasks',
    icon: 'mdi-clock',
    section: 'system',
    permission: null,
    order: 1,
  };
  const app = mount(
    t,
    'App.vue',
    {},
    {
      api: async (path) => {
        assert.equal(path, '/plugins/navigation');
        calls++;
        return [plugin];
      },
    },
  );
  app.state.starting.value = false;
  app.state.auth.value = { authenticated: true };
  await app.state.pluginNavigation.ensure();
  await app.state.pluginNavigation.ensure();
  const nodes = app.nodes();
  assert.equal(
    nodes.some((node) => node.type === 'aside'),
    false,
  );
  assert.ok(
    nodes.some((node) => node.props?.['aria-label'] === '退出登录'),
    'original logout icon stays reachable',
  );
  const footerNode = nodes.find((node) => node.type?.name === 'AppModeFooter');
  assert.ok(footerNode);
  const footer = mount(t, 'components/AppModeFooter.vue', footerNode.props);
  assert.ok(footer.nodes().some((node) => node.props?.href === '#apps'));
  const centerNode = nodes.find((node) => node.type?.name === 'PluginApplicationCenter');
  assert.equal(
    centerNode.props.plugins,
    app.state.pluginNavItems.items,
    'same reactive cache object reaches app center',
  );
  const center = mount(t, 'components/PluginApplicationCenter.vue', vue.reactive(centerNode.props));
  assert.ok(center.nodes().some((node) => node.props?.href === '#plugin-app/Demo_clone/tasks'));
  assert.ok(center.nodes().some((node) => node.props?.href === '#settings'));
  assert.ok(
    !center.nodes().some((node) => node.props?.href === '#dashboard'),
    'footer entries are excluded',
  );
  assert.equal(calls, 1);
  app.state.quickAccessOpen.value = true;
  assert.equal(
    app.nodes().some((node) => node.type?.name === 'AppModeFooter'),
    false,
  );
  modes.setInterfaceMode('desktop', { setItem() {} });
  assert.ok(app.nodes().some((node) => node.type === 'aside'));
  assert.equal(
    app.nodes().some((node) => node.type?.name === 'AppModeFooter'),
    false,
  );
});
