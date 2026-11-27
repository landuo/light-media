import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8');
const { descriptor } = parse(source);
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'loading' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
};
async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await vue.nextTick();
}
function mount(overrides = {}, hash = '#discover') {
  const ui = vue.reactive({ status: null, settings: null });
  const requests = [],
    feed = [],
    timers = [],
    signals = [],
    routes = [];
  let ensures = 0;
  const api = async (path, body, method, signal) => {
    requests.push(path);
    if (signal) signals.push(signal);
    if (path in overrides)
      return typeof overrides[path] === 'function' ? overrides[path]() : overrides[path];
    if (path === '/auth/status') return { authenticated: true, setup_required: false };
    if (path === '/ready') return { starting: false };
    if (path === '/settings') return { sites: [{ id: 'one' }] };
    return {};
  };
  const module = { exports: {} },
    scope = vue.effectScope();
  let unmount;
  new Function('require', 'exports', 'location', 'setTimeout', 'clearTimeout', compiled)(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          onMounted() {},
          onUnmounted(fn) {
            unmount = fn;
          },
        };
      if (name === './api')
        return {
          ui,
          api,
          action: (fn) => fn().catch(() => undefined),
          errorText: String,
          go: (path) => routes.push(path),
        };
      if (name === './features/bounded-read') return { boundedRead: (fn, signal) => fn(signal) };
      if (name === './features/plugin-navigation')
        return {
          createPluginNavigation: () => ({ state: {}, ensure: () => ensures++, reset() {} }),
        };
      if (name === './features/system-messages')
        return {
          createSystemMessageFeed: () => ({
            setAuthenticated: (value) => feed.push(value),
            destroy() {},
          }),
        };
      if (name === './features/search-session')
        return { searchSession: {}, clearSearchSession() {} };
      if (name === './features/plugin-remote-api') return { abortPluginAPIRequests() {} };
      return {};
    },
    module.exports,
    { hash },
    (fn, ms) => {
      const timer = { fn, ms };
      timers.push(timer);
      return timer;
    },
    (timer) => {
      if (timer) timer.canceled = true;
    },
  );
  const app = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  return {
    app,
    ui,
    requests,
    feed,
    timers,
    signals,
    routes,
    ensures: () => ensures,
    close() {
      app.expired();
      scope.stop();
    },
  };
}

test('bulk read acknowledges the clicked snapshot once and refreshes durable badges', async () => {
  const write = deferred();
  const h = mount({
    '/notifications/read-all': write.promise,
    '/notifications': { items: [{ id: 'late', read: false }], unread: 1 },
  });
  await h.app.bootstrap();
  h.app.notificationsOpen.value = true;
  h.app.systemMessages.value = { items: [{ id: 10 }], hasNew: true };
  const marked = [];
  h.app.systemMessageFeed.markAllRead = (id) => marked.push(id);
  const first = h.app.readAllNotifications();
  await h.app.readAllNotifications();
  h.app.systemMessages.value = { items: [{ id: 11 }, { id: 10 }], hasNew: true };
  write.resolve({ updated: 103 });
  await first;
  assert.equal(h.requests.filter((path) => path === '/notifications/read-all').length, 1);
  assert.deepEqual(marked, [10]);
  assert.equal(h.app.notificationsOpen.value, false);
  assert.equal(h.app.hasUnreadTasks.value, true);
  assert.equal(h.app.notifications.value.unread, 1);
  h.close();
});

test('failed bulk read keeps the panel and unread items for retry', async () => {
  const h = mount({ '/notifications/read-all': () => Promise.reject(new Error('offline')) });
  await h.app.bootstrap();
  h.app.notificationsOpen.value = true;
  h.app.hasUnreadTasks.value = true;
  let marked = false;
  h.app.systemMessageFeed.markAllRead = () => {
    marked = true;
  };
  await h.app.readAllNotifications();
  assert.equal(marked, false);
  assert.equal(h.app.notificationsOpen.value, true);
  assert.equal(h.app.hasUnreadTasks.value, true);
  assert.equal(h.app.markingNotificationsRead.value, false);
  h.close();
});

test('opening a bell lit only by a background task immediately selects the task tab', async () => {
  const list = deferred();
  const h = mount({
    '/status?view=navigation': { has_unread: true },
    '/notifications': list.promise,
  });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.hasUnreadTasks.value, true);
  const opening = h.app.toggleNotifications();
  assert.equal(h.app.notificationTab.value, 'tasks');
  assert.equal(h.app.notificationsLoading.value, true);
  list.resolve({ unread: 1, items: [{ id: 'task', title: '后台通知', read: false }] });
  await opening;
  assert.equal(h.app.notifications.value.items[0].id, 'task');
  assert.equal(h.app.notificationsLoading.value, false);
  await h.app.toggleNotifications();
  assert.equal(h.requests.filter((r) => r === '/notifications').length, 1);
  h.close();
});

test('a late task count selects the unread tab unless the reader selected a tab first', async () => {
  for (const manual of [false, true]) {
    const list = deferred();
    const h = mount({ '/notifications': list.promise });
    await h.app.bootstrap();
    const opening = h.app.toggleNotifications();
    if (manual) h.app.selectNotificationTab('system');
    list.resolve({ unread: 1, items: [{ id: 'one', read: false }] });
    await opening;
    assert.equal(h.app.notificationTab.value, manual ? 'system' : 'tasks');
    h.close();
  }
});

test('an unread system message opens its tab even when there are also unread tasks', async () => {
  const h = mount({ '/notifications': { unread: 2, items: [{ id: 'one' }, { id: 'two' }] } });
  await h.app.bootstrap();
  h.app.systemMessages.value = { items: [{ id: 1, read: false }], hasNew: true };
  h.app.hasUnreadTasks.value = true;
  await h.app.toggleNotifications();
  assert.equal(h.app.notificationTab.value, 'system');
  h.close();
});

test('notification history errors retain the unread badge until a successful retry', async () => {
  let fail = true;
  const h = mount({
    '/status?view=navigation': { has_unread: true },
    '/notifications': () => {
      if (fail) throw Error('history read failed');
      return { unread: 0, items: [] };
    },
  });
  await h.app.bootstrap();
  await flush();
  await h.app.toggleNotifications();
  assert.match(h.app.notificationError.value, /history read failed/);
  assert.equal(h.app.notificationsLoading.value, false);
  assert.equal(h.app.hasUnreadTasks.value, true);
  fail = false;
  await h.app.loadNotifications();
  assert.equal(h.app.notificationError.value, '');
  assert.equal(h.app.hasUnreadTasks.value, false);
  h.close();
});

test('slow settings and statistics do not block independent browsing or plugin startup', async () => {
  const settings = deferred(),
    stats = deferred();
  const h = mount({ '/settings': settings.promise, '/status?view=navigation': stats.promise });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.starting.value, false);
  assert.equal(h.app.pageReady.value, true);
  assert.equal(h.ui.settings, null);
  assert.equal(h.ui.status, null);
  assert.equal(h.ensures(), 1);
  assert.equal(h.requests.includes('/notifications'), false);
  settings.resolve({ sites: [] });
  stats.resolve({ counts: { active_jobs: 1 } });
  await flush();
  assert.equal(h.ui.status.counts.active_jobs, 1);
  h.close();
});

test('settings-dependent selections remain gated and failed settings have an independent retry', async () => {
  let failed = true;
  const h = mount(
    {
      '/settings': () =>
        failed ? Promise.reject(new Error('settings unavailable')) : { sites: [] },
    },
    '#resources',
  );
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.businessReady.value, true);
  assert.equal(h.app.pageReady.value, false);
  assert.match(h.app.settingsError.value, /settings unavailable/);
  failed = false;
  await h.app.loadSettings();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  h.close();
});

test('startup waits for authenticated lightweight readiness, polls serially at 1.5 seconds, then stops', async () => {
  let starting = true;
  const h = mount({ '/ready': () => ({ starting }) });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.serviceStarting.value, true);
  assert.equal(h.app.businessReady.value, false);
  assert.equal(h.ensures(), 0);
  assert.equal(h.requests.includes('/status?view=navigation'), false);
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].ms, 1500);
  starting = false;
  h.timers[0].fn();
  await flush();
  assert.equal(h.app.businessReady.value, true);
  assert.equal(h.ensures(), 1);
  assert.equal(h.timers.length, 1);
  h.close();
});

test('failed or malformed readiness stays closed, offers retry and does not start feeds', async () => {
  let result = Promise.reject(new Error('ready unavailable'));
  const h = mount({ '/ready': () => result });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.businessReady.value, false);
  assert.match(h.app.readinessError.value, /unavailable/);
  assert.equal(h.ensures(), 0);
  assert.equal(h.timers.length, 0);
  result = {};
  await h.app.checkReadiness();
  assert.equal(h.app.businessReady.value, false);
  result = { starting: false };
  await h.app.checkReadiness();
  await flush();
  assert.equal(h.app.businessReady.value, true);
  h.close();
});

test('unauthenticated users never request settings, readiness, statistics or plugin navigation', async () => {
  const h = mount({ '/auth/status': { authenticated: false, setup_required: false } });
  await h.app.bootstrap();
  await flush();
  assert.deepEqual(h.requests, ['/auth/status']);
  assert.equal(h.app.pageReady.value, false);
  h.close();
});

test('logout cancels in-flight startup requests and ignores late data from the old session', async () => {
  const settings = deferred(),
    ready = deferred();
  const h = mount({ '/settings': settings.promise, '/ready': ready.promise });
  const boot = h.app.bootstrap();
  await flush();
  h.app.expired();
  assert.ok(h.signals.every((signal) => signal.aborted));
  settings.resolve({ sites: ['stale'] });
  ready.resolve({ starting: false });
  await boot;
  await flush();
  assert.equal(h.ui.settings, null);
  assert.equal(h.app.pageReady.value, false);
  assert.equal(h.ensures(), 0);
  h.close();
});

test('login does not redirect while background settings are unknown', async () => {
  const settings = deferred();
  const h = mount({ '/settings': settings.promise });
  h.app.auth.value = { authenticated: false, setup_required: false };
  await h.app.login();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  assert.deepEqual(h.routes, []);
  settings.resolve({ sites: [{ id: 'one' }] });
  await flush();
  h.close();
});

test('business pages retain KeepAlive boundaries, local gates and logs while starting', () => {
  assert.match(source, /<template v-if="pageReady">[\s\S]*<KeepAlive/);
  assert.match(source, /<Operations v-if="serviceStarting && pageKey === 'logs'" view="logs"/);
  assert.match(source, /const Explore = defineAsyncComponent/);
  assert.match(source, /<KeepAlive :max="24">\s*<component :is="searchComponent"/);
  assert.match(source, /@click="checkReadiness"/);
  assert.match(source, /@click="loadSettings"/);
});

test('auth read failures remain unauthenticated and retry recovers without settings coupling', async () => {
  let failed = true;
  const h = mount({
    '/auth/status': () => (failed ? Promise.reject(new Error('offline')) : { authenticated: true }),
  });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.auth.value, null);
  assert.equal(h.app.pageReady.value, false);
  assert.match(h.app.authError.value, /offline/);
  failed = false;
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  assert.equal(h.app.authError.value, '');
  h.close();
});

test('new setup still opens settings after login even while settings are loading', async () => {
  const settings = deferred();
  const h = mount({ '/settings': settings.promise });
  h.app.auth.value = { authenticated: false, setup_required: true };
  await h.app.login();
  await flush();
  assert.ok(h.requests.includes('/auth/setup'));
  assert.deepEqual(h.routes, ['settings']);
  settings.resolve({ sites: [] });
  await flush();
  h.close();
});

test('new bootstrap supersedes old settings and readiness responses', async () => {
  const oldSettings = deferred(),
    oldReady = deferred();
  let newer = false;
  const h = mount({
    '/settings': () => (newer ? { sites: ['new'] } : oldSettings.promise),
    '/ready': () => (newer ? { starting: false } : oldReady.promise),
  });
  const first = h.app.bootstrap();
  await flush();
  newer = true;
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  oldSettings.resolve({ sites: ['old'] });
  oldReady.resolve({ starting: true });
  await first;
  await flush();
  assert.deepEqual([...h.ui.settings.sites], ['new']);
  assert.equal(h.app.pageReady.value, true);
  assert.equal(h.timers.length, 0);
  h.close();
});

test('a running server restart closes the business gate and resumes lightweight readiness probes', async () => {
  let restarting = false;
  const h = mount({
    '/ready': () => ({ starting: restarting }),
    '/status?view=navigation': () => ({ starting: restarting }),
  });
  await h.app.bootstrap();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  restarting = true;
  await h.app.loadStatus();
  await flush();
  assert.equal(h.app.businessReady.value, false);
  assert.equal(h.timers.length, 1);
  restarting = false;
  h.timers[0].fn();
  await flush();
  assert.equal(h.app.pageReady.value, true);
  h.close();
});
