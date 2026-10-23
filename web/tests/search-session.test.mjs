import test from 'node:test';
import assert from 'node:assert/strict';
import { effectScope, nextTick, ref } from 'vue';
import {
  startSearch,
  useExplicitSearch,
  clearSearchSession,
  searchSession,
} from '../src/features/search-session.ts';

function setup(t) {
  const old = globalThis.location;
  globalThis.location = { hash: '#search' };
  clearSearchSession();
  const scope = effectScope();
  t.after(() => {
    scope.stop();
    clearSearchSession();
    globalThis.location = old;
  });
  return scope;
}

test('navigation and an edited draft do not submit; a repeated explicit search submits exactly once', async (t) => {
  const scope = setup(t),
    route = 'search?query=诛仙';
  const calls = [];
  scope.run(() => useExplicitSearch(route, () => calls.push(route)));
  location.hash = route;
  await nextTick();
  assert.equal(calls.length, 0);
  startSearch(route);
  await nextTick();
  assert.equal(calls.length, 1);
  location.hash = 'sites';
  location.hash = searchSession.lastRoute;
  await nextTick();
  assert.equal(calls.length, 1);
  // Cached state may be an empty result or a failed search: navigation is still passive.
  startSearch(route);
  await nextTick();
  assert.equal(calls.length, 2);
  assert.equal(searchSession.lastRoute, route);
});

test('a request waits for initialization and is never replayed by a remount or navigation', async (t) => {
  const scope = setup(t),
    route = 'resources/tv/42',
    ready = ref(false);
  let calls = 0;
  startSearch(route);
  scope.run(() => useExplicitSearch(route, () => calls++, ready));
  await nextTick();
  assert.equal(calls, 0);
  ready.value = true;
  await nextTick();
  assert.equal(calls, 1);
  scope.run(() => useExplicitSearch(route, () => calls++));
  await nextTick();
  assert.equal(calls, 1);
  startSearch('search?query=other');
  await nextTick();
  assert.equal(calls, 1);
});

test('logout clears the remembered target and cannot replay an earlier user search', async (t) => {
  const scope = setup(t),
    route = 'resource-search?query=诛仙';
  startSearch(route);
  clearSearchSession();
  let calls = 0;
  scope.run(() => useExplicitSearch(route, () => calls++));
  await nextTick();
  assert.equal(calls, 0);
  assert.equal(searchSession.lastRoute, 'search');
});
