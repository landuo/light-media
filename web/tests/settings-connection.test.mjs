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

async function mount(t, result) {
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
  };
}

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
