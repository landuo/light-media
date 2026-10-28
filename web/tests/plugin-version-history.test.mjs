import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import { renderPluginHistory } from '../src/features/plugin-history-markdown.ts';
const { descriptor } = parse(readFileSync(new URL('../src/components/PluginVersionHistoryDialog.vue', import.meta.url), 'utf8'));
const script = compileScript(descriptor, { id: 'PluginVersionHistory' });
const compiled = ts.transpileModule(script.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
function fixture(value, plugin = { instance_id: 'Foo', name: 'Foo', repo_url: 'https://owned.invalid/index' }) {
  const requests = [], emitted = [];
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue') return { ...vue, onMounted: () => {} };
    if (name === '../api') return {
      api: async (path) => { requests.push(path); if (value instanceof Error) throw value; return value; },
      errorText: (e) => e.message,
    };
    if (name.startsWith('vuetify/') || name.endsWith('.vue')) return {};
    if (name === '../features/plugin-history-markdown') return { renderPluginHistory };
    throw new Error(name);
  }, module.exports);
  return { requests, emitted, state: module.exports.default.setup(plugin ? { plugin } : {}, { expose() {}, emit: (...args) => emitted.push(args) }) };
}
test('pinned history dialog requests author notes without a repository override or a release picker', async () => {
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: 'PluginVersionHistoryDialog.vue', id: 'history', compilerOptions: { bindingMetadata: script.bindings } }).errors, []);
  const response = { history: { '2.0': '作者说明' }, items: [{ version: '2.0', is_latest: true, is_current: false }], rejected: [{ version: '0.1', reason: '平台不兼容' }] };
  const f = fixture(response);
  await f.state.load();
  const url = new URL(f.requests[0], 'https://owned.invalid');
  assert.equal(url.pathname, '/plugins/history/Foo');
  assert.equal(url.searchParams.get('repo_url'), null);
  assert.equal(url.searchParams.get('force'), 'true');
  assert.deepEqual(f.state.notes.value, [['2.0', '作者说明']]);
  assert.doesNotMatch(descriptor.template.content, /仓库可用版本|result\.items|result\.rejected/);
  assert.match(descriptor.template.content, /v-if="showUpdateAction"/);
  assert.equal(f.requests.length, 1);
});
test('clone history binds its exact instance without letting copied source provenance override configured sources', async () => {
  const f = fixture({ history: {}, items: [], rejected: [] }, { instance_id: 'Foowork', name: 'Work', source_instance_id: 'Foo', repo_url: 'https://owned.invalid/foo' });
  await f.state.load();
  assert.equal(new URL(f.requests[0], 'https://owned.invalid').searchParams.get('repo_url'), null);
  assert.match(f.requests[0], /history\/Foowork/);
});
test('history failures remain visible and do not install or fabricate entries', async () => {
  for (const value of [new Error('来源读取失败'), { history: [] }, { history: { v1: {} } }]) {
    const f = fixture(value);
    await f.state.load();
    assert.notEqual(f.state.error.value, '');
    assert.equal(f.state.result.value, undefined);
    assert.equal(f.state.loading.value, false);
    assert.deepEqual(f.emitted, []);
    assert.equal(f.requests.length, 1);
  }
});
test('history Markdown follows fixed VersionHistory options for formatting, HTML and safe new-window links', () => {
  const html = renderPluginHistory('## 修复\n\n**初始化**\n\n- 来源\n- 分身\n\n[项目](https://example.org/plugin)\n\n`v2`\n\n<span>作者 HTML</span>');
  assert.match(html, /<h2>修复<\/h2>/);
  assert.match(html, /<strong>初始化<\/strong>/);
  assert.match(html, /<li>来源<\/li>/);
  assert.match(html, /target="_blank"/);
  assert.match(html, /rel="noopener noreferrer"/);
  assert.match(html, /<code>v2<\/code>/);
  assert.match(html, /<span>作者 HTML<\/span>/);
});
