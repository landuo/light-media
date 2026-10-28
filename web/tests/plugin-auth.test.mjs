import './helpers/plugin-axios-fetch-adapter.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { createPluginAuth, pluginAuthProviders } from '../src/features/plugin-auth.ts';
import { parsePluginFrontendDescriptor } from '../src/features/plugin-remote-contract.ts';

const digest = 'a'.repeat(64);
const provider = { id: 'plugin:OwnedAuth', name: 'Owned auth', type: 'plugin', plugin_id: 'OwnedAuth', enabled: true, remote: { id: 'OwnedAuth', url: `/plugins/OwnedAuth/frontend/${digest}/assets/remoteEntry.js` } };
const descriptor = { instance_id: 'OwnedAuth', plugin_id: 'OwnedAuth', version: '1.0', package_sha256: digest, contract: 'light-media.vue-federation/v1', api_contract: 'light-media.plugin-api/v1', exposes: ['AuthPage', 'CustomLogin'], shared: ['vue'], entry_path: provider.remote.url };
const component = { render() {} };
function fixture(overrides = {}) {
  const calls = [];
  const controller = createPluginAuth({
    providers: async () => [provider],
    load: async (...args) => { calls.push(['load', ...args]); return { component, descriptor }; },
    exchange: async (...args) => { calls.push(['exchange', ...args]); return { authenticated: true }; },
    authenticated: () => calls.push(['authenticated']),
    ...overrides,
  });
  return { controller, calls };
}
function deferred() { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; }

test('only usable declared plugin providers become buttons', () => {
  assert.deepEqual(pluginAuthProviders([provider, { ...provider, enabled: false }, { ...provider, type: 'system' }, { ...provider, remote: { id: 'Another', url: provider.remote.url } }, { ...provider, component: '../AuthPage' }, null]), [provider]);
  assert.throws(() => pluginAuthProviders({ success: true }));
  assert.equal(parsePluginFrontendDescriptor(descriptor, 'OwnedAuth', 'CustomLogin').entry_path, provider.remote.url);
  assert.throws(() => parsePluginFrontendDescriptor(descriptor, 'OwnedAuth', 'Undeclared'));
  assert.throws(() => parsePluginFrontendDescriptor({ ...descriptor, exposes: ['../AuthPage'] }, 'OwnedAuth', '../AuthPage'));
});
test('default AuthPage receives exact instance and only confirmed exchange logs in', async () => {
  const { controller: c, calls } = fixture();
  await c.refresh(); await c.open(c.state.providers[0]);
  assert.equal(calls[0][1], 'OwnedAuth'); assert.equal(calls[0][2], 'AuthPage');
  await c.authenticate({ ticket: 'one-time-fixture' });
  assert.deepEqual(calls.map(x => x[0]), ['load', 'exchange', 'authenticated']);
  assert.equal(c.state.selected, null);
});
test('missing ticket and unsuccessful exchange stay visible and never announce login', async () => {
  const { controller: c, calls } = fixture({ exchange: async () => ({ success: true }) });
  await c.refresh(); await c.open(c.state.providers[0]);
  await c.authenticate({}); assert.match(c.state.error, /票据/);
  await c.authenticate({ ticket: 'not-confirmed' }); assert.match(c.state.error, /未完成/);
  assert.equal(calls.some(x => x[0] === 'authenticated'), false);
  assert.ok(c.state.component);
});
test('closing or choosing another provider discards stale imports and aborts requests', async () => {
  const pending = deferred();
  const { controller: c } = fixture({ load: () => pending.promise });
  await c.refresh(); const open = c.open(c.state.providers[0]); const signal = c.state.signal;
  c.close(); pending.resolve({ component, descriptor }); await open;
  assert.ok(signal.aborted); assert.equal(c.state.component, null); assert.equal(c.state.selected, null);
});
test('changed package URL refuses stale provider metadata', async () => {
  const { controller: c } = fixture({ load: async () => ({ component, descriptor: { ...descriptor, entry_path: descriptor.entry_path.replace(digest, 'b'.repeat(64)) } }) });
  await c.refresh(); await c.open(c.state.providers[0]);
  assert.match(c.state.error, /版本已变化/); assert.equal(c.state.component, null);
});
test('one pending exchange cannot be replayed by duplicate component events', async () => {
  const pending = deferred(); let exchanges = 0;
  const { controller: c, calls } = fixture({ exchange: async () => { exchanges++; return pending.promise; } });
  await c.refresh(); await c.open(c.state.providers[0]);
  const first = c.authenticate({ ticket: 'single' }); await c.authenticate({ ticket: 'single' });
  assert.equal(exchanges, 1); pending.resolve({ authenticated: true }); await first;
  assert.equal(calls.filter(x => x[0] === 'authenticated').length, 1);
});
test('provider retrieval failure is retryable and unmount suppresses late results', async () => {
  let fail = true; const { controller: c } = fixture({ providers: async () => { if (fail) throw new Error('offline'); return [provider]; } });
  await c.refresh(); assert.equal(c.state.providersError, 'offline');
  fail = false; await c.refresh(); assert.equal(c.state.providers.length, 1); assert.equal(c.state.providersError, '');
  c.destroy(); await c.refresh(); assert.equal(c.state.selected, null);
});

test('clone descriptor retains compiled source identity while Axios preserves requested literal identities', async () => {
  const { remotePluginAPIPath } = await import('../src/features/plugin-remote-api.ts');
  const clone = { ...provider, id: 'plugin:OwnedAuthlogin', plugin_id: 'OwnedAuthlogin', remote: { id: 'OwnedAuthlogin', url: provider.remote.url.replace('/OwnedAuth/', '/OwnedAuthlogin/') } };
  const { controller: c } = fixture({ providers: async () => [clone], load: async () => ({ component, descriptor: { ...descriptor, instance_id: clone.plugin_id, entry_path: clone.remote.url } }) });
  await c.refresh(); await c.open(c.state.providers[0]);
  assert.equal(c.state.sourcePluginID, 'OwnedAuth');
  for (const prefix of ['OwnedAuth', 'OwnedAuthlogin'])
    assert.equal(remotePluginAPIPath(c.state.sourcePluginID, clone.plugin_id, `plugin/${prefix}/authenticate`, 'POST'), `/v1/plugin/${prefix}/authenticate`);
  assert.equal(remotePluginAPIPath(c.state.sourcePluginID, clone.plugin_id, 'plugin/Another/authenticate', 'POST'), '/v1/plugin/Another/authenticate');
});

test('component retry refreshes providers so an upgraded package can load without page reload', async () => {
  const nextURL = provider.remote.url.replace(digest, 'b'.repeat(64));
  let fetches = 0;
  const { controller: c } = fixture({
    providers: async () => { fetches++; return [{ ...provider, remote: { ...provider.remote, url: fetches === 1 ? provider.remote.url : nextURL } }]; },
    load: async () => ({ component, descriptor: { ...descriptor, entry_path: nextURL, package_sha256: 'b'.repeat(64) } }),
  });
  await c.refresh(); await c.open(c.state.providers[0]);
  assert.match(c.state.error, /版本已变化/);
  await c.retry();
  assert.equal(fetches, 2); assert.equal(c.state.error, ''); assert.equal(c.state.component, component);
});
