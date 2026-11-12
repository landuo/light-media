import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8');
test('startup defers plugin navigation and feeds until status becomes ready, then recovers automatically', async () => {
  const { descriptor } = parse(source);
  const compiled = ts.transpileModule(compileScript(descriptor, { id: 'loading' }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const ui = vue.reactive({ status: null, settings: null });
  const requests = [], feed = [];
  let ensures = 0;
  const api = async path => { requests.push(path); return path === '/auth/status' ? { authenticated: true } : {}; };
  const module = { exports: {} };
  const scope = vue.effectScope();
  new Function('require', 'exports', 'location', compiled)(name => {
    if (name === 'vue') return { ...vue, onMounted() {}, onUnmounted() {} };
    if (name === './api') return { ui, api, refreshSettings: async () => { ui.settings = {}; }, refreshStatus: async () => { ui.status = { starting: true }; } };
    if (name === './features/plugin-navigation') return { createPluginNavigation: () => ({ state: {}, ensure: () => ensures++, reset() {} }) };
    if (name === './features/system-messages') return { createSystemMessageFeed: () => ({ setAuthenticated: value => feed.push(value) }) };
    if (name === './features/search-session') return { searchSession: {} };
    return {};
  }, module.exports, { hash: '#discover' });
  const app = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  await app.bootstrap(); await vue.nextTick();
  assert.equal(app.serviceStarting.value, true);
  assert.equal(app.businessReady.value, false);
  assert.equal(ensures, 0);
  assert.equal(requests.includes('/notifications'), false);
  assert.equal(feed.includes(true), false);
  ui.status = { starting: false }; await vue.nextTick();
  assert.equal(app.businessReady.value, true);
  assert.equal(ensures, 1);
  assert.equal(requests.includes('/notifications'), true);
  assert.equal(feed.at(-1), true);
  ui.status = { starting: false }; await vue.nextTick();
  assert.equal(ensures, 1, 'ordinary polls do not remount/reinitialize the ready app');
  scope.stop();
});

test('business pages retain KeepAlive boundaries and logs remain accessible while starting', () => {
  assert.match(source, /<template v-if="!serviceStarting">[\s\S]*<KeepAlive/);
  assert.match(source, /<Operations v-if="serviceStarting && pageKey === 'logs'" view="logs"/);
  assert.match(source, /const Explore = defineAsyncComponent/);
  assert.match(source, /const FileManager = defineAsyncComponent/);
  assert.match(source, /<KeepAlive :max="24">\s*<component :is="searchComponent"/);
});
