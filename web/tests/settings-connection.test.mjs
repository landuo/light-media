import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const copy = (value) => JSON.parse(JSON.stringify(value));
const { descriptor } = parse(
  readFileSync(new URL('../src/components/Settings.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'Settings', inlineTemplate: true });
const source = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function nodeText(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(nodeText).join(' ');
  return nodeText(node.children);
}

async function mount(t, result, initial = {}) {
  let settings = {
    tmdb: { api_key: '', access_token: '' },
    fnos: {
      host: 'http://nas.invalid:5666/v',
      username: 'synthetic-user',
      password: '',
      enabled: true,
    },
    secret_state: { fnos: true },
    sites: [],
    ...initial,
  };
  const fixture = { requests: [], toasts: [], failSave: false, result },
    mounted = [],
    unmounted = [],
    pending = [];
  const toast = (message, error = false) => fixture.toasts.push({ message, error });
  const api = async (path, body, method) => {
    fixture.requests.push({ path, body: body === undefined ? undefined : copy(body), method });
    if (path === '/settings/logging') return { level: 'INFO', debug_overrides: [] };
    if (path === '/settings' && method === 'PUT') {
      if (fixture.failSave) throw new Error('设置保存失败');
      settings = { ...copy(body), secret_state: { fnos: true } };
      return copy(settings);
    }
    // The API wrapper has already accepted HTTP 200 here. The returned probe
    // status still determines whether the connection succeeded.
    if (path === '/settings/test') return copy(fixture.result);
    throw new Error('Unexpected request ' + path);
  };
  const action = (callback, success) => {
    const operation = (async () => {
      try {
        const value = await callback();
        if (success) toast(success);
        return value;
      } catch (error) {
        toast(error.message, true);
      }
    })();
    pending.push(operation);
    return operation;
  };
  const module = { exports: {} };
  new Function('require', 'exports', 'window', 'location', source)(
    (dependency) => {
      if (/^vuetify\/components\/(VTextField|VSelect|VTextarea)$/.test(dependency)) {
        const component = dependency.split('/').at(-1);
        return { [component]: { name: component } };
      }
      if (dependency === 'vue')
        return {
          ...vue,
          onMounted: (callback) => mounted.push(callback),
          onBeforeUnmount: (callback) => unmounted.push(callback),
          withDirectives: (node) => node,
        };
      if (dependency === '../api')
        return {
          api,
          action,
          toast,
          ui: { settings },
          refreshSettings: async () => copy(settings),
          errorText: (error) => error.message,
          date: String,
          bytes: String,
        };
      if (dependency.endsWith('.vue')) return { name: dependency };
      if (dependency === 'vuetify/components/VSwitch') return { VSwitch: 'source-switch' };
      throw new Error('Unexpected dependency ' + dependency);
    },
    module.exports,
    { addEventListener() {}, removeEventListener() {} },
    { hash: '#settings?tab=system' },
  );
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {}, emit() {} }));
  t.after(() => {
    unmounted.forEach((callback) => callback());
    scope.stop();
  });
  mounted.forEach((callback) => callback());
  await Promise.all(pending);
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
  return {
    fixture,
    nodes,
    async testConnection(target = 'fnos') {
      const title = target === 'fnos' ? '媒体服务器' : 'TMDB 元数据';
      const panel = nodes().find(
        (node) => node.type === 'section' && nodeText(node).includes(title),
      );
      assert.ok(panel, title);
      const button = panel.children
        .find((node) => node?.props?.class === 'button-row')
        .children.find((node) => node.type === 'button' && nodeText(node).trim() === '测试');
      assert.ok(button, title + '测试按钮');
      await button.props.onClick();
      await vue.nextTick();
    },
    resultMessage() {
      return nodes().find((node) => node.props?.role === 'status');
    },
    readMessages() {
      return nodes().find(
        (node) => node.type === 'source-switch' && node.props?.label === '阅读站点消息',
      );
    },
    async saveReadMessages(value) {
      const field = nodes().find(
        (node) => node.type === 'source-switch' && node.props?.label === '阅读站点消息',
      );
      assert.ok(field);
      field.props['onUpdate:modelValue'](value);
      await vue.nextTick();
      const panel = nodes().find(
        (node) => node.type === 'section' && nodeText(node).includes('站点选项'),
      );
      const button = panel.children
        .find((node) => node?.props?.class === 'button-row')
        .children.find((node) => node.type === 'button' && nodeText(node).trim() === '保存');
      await button.props.onClick();
      await vue.nextTick();
    },
  };
}

test('inherited source false is displayed without changing the nullable preference on an unrelated save', async (t) => {
  const app = await mount(
    t,
    { status: 'passed' },
    {
      read_site_messages: null,
      effective_read_site_messages: false,
    },
  );
  assert.equal(app.readMessages().props.modelValue, false);
  await app.testConnection();
  const request = app.fixture.requests.find((r) => r.path === '/settings' && r.method === 'PUT');
  assert.equal(request.body.read_site_messages, null);
  await app.saveReadMessages(true);
  assert.equal(
    app.fixture.requests.filter((r) => r.path === '/settings' && r.method === 'PUT').at(-1).body
      .read_site_messages,
    true,
  );
});

test('an unconfirmed source setting stays unknown, disables its switch and cannot be silently saved as true', async (t) => {
  const message = '站点消息设置无法确认，请检查迁移数据；站点刷新已停止读取消息';
  const app = await mount(
    t,
    {},
    {
      read_site_messages: null,
      effective_read_site_messages: null,
      read_site_messages_error: message,
    },
  );
  assert.equal(app.readMessages().props.modelValue, null);
  assert.equal(app.readMessages().props.disabled, true);
  assert.ok(app.nodes().some((node) => node.props?.role === 'alert' && nodeText(node) === message));
  await app.saveReadMessages(true);
  assert.equal(
    app.fixture.requests.filter((r) => r.path === '/settings' && r.method === 'PUT').length,
    0,
  );
  assert.deepEqual(app.fixture.toasts.at(-1), { message, error: true });
});

test('the source read-site-messages switch saves explicit false and true with the site interval', async (t) => {
  const app = await mount(t, {});
  for (const enabled of [false, true]) {
    await app.saveReadMessages(enabled);
    const request = app.fixture.requests
      .filter((r) => r.path === '/settings' && r.method === 'PUT')
      .at(-1);
    assert.equal(request.body.read_site_messages, enabled);
    assert.equal(request.body.site_data_refresh_hours, 6);
    assert.equal(request.body.fnos.host, 'http://nas.invalid:5666/v');
  }
});

test('HTTP 200 with a failed TLS probe shows its actionable message as an error', async (t) => {
  const message = '飞牛影视 TLS 证书校验失败，请使用与证书匹配的域名或改用内网 HTTP 地址';
  const app = await mount(t, { status: 'failed', code: 'TLS_CERTIFICATE_FAILED', message });
  await app.testConnection();

  assert.equal(nodeText(app.resultMessage()), message);
  assert.deepEqual(app.fixture.toasts.at(-1), { message, error: true });
  assert.deepEqual(
    app.fixture.requests
      .filter((request) => request.path !== '/settings/logging')
      .map((request) => request.path),
    ['/settings', '/settings/test'],
  );
  assert.deepEqual(app.fixture.requests.at(-1).body, { target: 'fnos' });
});

test('a failed save clears the old probe result and does not test stale settings', async (t) => {
  const app = await mount(t, { status: 'passed', message: '旧地址连接成功' });
  await app.testConnection();
  assert.equal(nodeText(app.resultMessage()), '旧地址连接成功');
  app.fixture.failSave = true;
  await app.testConnection();

  assert.equal(app.resultMessage(), undefined);
  assert.equal(
    app.fixture.requests.filter((request) => request.path === '/settings/test').length,
    1,
  );
  assert.deepEqual(app.fixture.toasts.at(-1), { message: '设置保存失败', error: true });
});

test('passed and ok probe statuses show the connection message without an error', async (t) => {
  for (const [target, status] of [
    ['fnos', 'passed'],
    ['tmdb', 'ok'],
  ]) {
    const message = target === 'fnos' ? '飞牛影视连接成功' : 'TMDB 连接成功';
    const app = await mount(t, { status, message });
    await app.testConnection(target);

    assert.equal(nodeText(app.resultMessage()), message);
    assert.deepEqual(app.fixture.toasts.at(-1), { message, error: false });
    assert.deepEqual(app.fixture.requests.at(-1).body, { target });
  }
});
