import test from 'node:test';
import assert from 'node:assert/strict';
import { api, APIError, errorText, ui } from '../src/api.ts';

function useResponses(t, responses) {
  const original = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (...request) => {
    requests.push(request);
    assert.ok(responses.length, 'unexpected request');
    return responses.shift();
  };
  t.after(() => {
    globalThis.fetch = original;
    assert.equal(ui.busy, 0, 'failed and successful calls both clear busy state');
  });
  return requests;
}

test('original plain-text market failure is visible and the next normal request recovers', async (t) => {
  const cards = [{ plugin_id: 'OwnedRecovery', name: 'Owned recovery' }];
  const requests = useResponses(t, [
    new Response('Internal Server Error', { status: 500, headers: { 'Content-Type': 'text/plain; charset=utf-8' } }),
    Response.json({ data: cards }),
  ]);
  await assert.rejects(api('/plugins/market'), (e) => {
    assert.ok(e instanceof APIError);
    assert.equal(e.code, 'request_failed');
    assert.equal(errorText(e), 'Internal Server Error');
    return true;
  });
  assert.equal(ui.busy, 0);
  assert.deepEqual(await api('/plugins/market'), cards);
  assert.equal(requests.length, 2);
  assert.equal(requests[0][1].credentials, 'same-origin');
});

test('JSON error envelope and request identity are unchanged', async (t) => {
  useResponses(t, [Response.json({ error: { code: 'OWNED_ERROR', message: 'owned failure' }, request_id: 'owned-request' }, { status: 409 })]);
  await assert.rejects(api('/plugins/market'), (e) => e instanceof APIError && e.code === 'OWNED_ERROR' && errorText(e) === 'owned failure · 请求 owned-request');
});

test('successful text and HTML errors remain invalid rather than silently succeeding', async (t) => {
  useResponses(t, [
    new Response('not JSON', { headers: { 'Content-Type': 'text/plain' } }),
    new Response('<html>owned server failure</html>', { status: 500, headers: { 'Content-Type': 'text/html' } }),
  ]);
  for (let i = 0; i < 2; i++) {
    await assert.rejects(api('/plugins/market'), (e) => e instanceof APIError && e.code === 'invalid_response');
  }
});

test('empty plain-text error stays failed and long diagnostics are display-bounded', async (t) => {
  useResponses(t, [
    new Response('', { status: 500, headers: { 'Content-Type': 'text/plain' } }),
    new Response('x'.repeat(4096), { status: 502, headers: { 'Content-Type': 'text/plain' } }),
  ]);
  await assert.rejects(api('/plugins/market'), (e) => e.code === 'request_failed' && e.message === '请求失败');
  await assert.rejects(api('/plugins/market'), (e) => e.code === 'request_failed' && e.message.length === 2048);
});

test('plain-text 401 retains session-expiration signal', async (t) => {
  const previous = globalThis.window;
  const events = [];
  globalThis.window = { dispatchEvent: (event) => events.push(event.type) };
  t.after(() => { globalThis.window = previous; });
  useResponses(t, [new Response('Unauthorized', { status: 401, headers: { 'Content-Type': 'text/plain' } })]);
  await assert.rejects(api('/plugins/market'), (e) => e.code === 'request_failed' && e.message === 'Unauthorized');
  assert.deepEqual(events, ['session-expired']);
});
