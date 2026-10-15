import test from 'node:test';
import assert from 'node:assert/strict';
import { reportFrontendError } from '../src/frontend-errors.ts';

test('frontend diagnostics send only fixed page/code and bounded reports', async (t) => {
  const originalFetch = globalThis.fetch;
  const originalLocation = globalThis.location;
  const requests = [];
  globalThis.location = { hash: '#detail/movie/278?token=synthetic-secret' };
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return new Response('{}');
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
    globalThis.location = originalLocation;
  });
  reportFrontendError('VUE_RENDER_ERROR');
  assert.equal(requests[0].path, '/api/frontend-errors');
  assert.equal(requests[0].options.credentials, 'same-origin');
  assert.deepEqual(JSON.parse(requests[0].options.body), {
    page: 'detail',
    code: 'VUE_RENDER_ERROR',
    request_id: '',
    build: '0.1.0',
    message: '页面渲染发生异常',
  });
  assert.ok(!requests[0].options.body.includes('278'));
  assert.ok(!requests[0].options.body.includes('synthetic-secret'));
  globalThis.location.hash = '#user-supplied-secret';
  reportFrontendError('UNHANDLED_REJECTION');
  assert.equal(JSON.parse(requests[1].options.body).page, 'home');
  for (let i = 0; i < 50; i++) reportFrontendError('SCRIPT_ERROR');
  assert.equal(requests.length, 10);
});
