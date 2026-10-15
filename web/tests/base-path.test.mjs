import test from 'node:test';
import assert from 'node:assert/strict';
import { appBasePath, apiURL } from '../src/base-path.ts';
import { api, image, searchStream } from '../src/api.ts';
import { keywordResourceSearch } from '../src/features/keyword-search.ts';
import { reportFrontendError } from '../src/frontend-errors.ts';

test('only the fixed proxy mount changes application URLs', () => {
  for (const path of ['/', '/index.html', '/light-media-other/', '//elsewhere.invalid/']) {
    assert.equal(appBasePath(path), '');
  }
  for (const path of ['/light-media', '/light-media/', '/light-media/index.html']) {
    assert.equal(appBasePath(path), '/light-media');
  }
  for (const path of ['https://elsewhere.invalid', '//elsewhere.invalid', 'settings']) {
    assert.throws(() => apiURL(path));
  }
});

test('all request types and download/image URLs retain the HTTPS mount', async (t) => {
  const oldLocation = globalThis.location;
  const oldFetch = globalThis.fetch;
  t.after(() => {
    globalThis.location = oldLocation;
    globalThis.fetch = oldFetch;
  });
  globalThis.location = { pathname: '/light-media/', hash: '#logs' };
  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return new Response(JSON.stringify({ data: { fixture: true } }), {
      headers: { 'Content-Type': 'application/json' },
    });
  };
  assert.deepEqual(await api('/auth/status'), { fixture: true });
  await searchStream({}, () => {}, new AbortController().signal);
  await keywordResourceSearch({ query: 'synthetic' }, new AbortController().signal);
  reportFrontendError('SCRIPT_ERROR');
  assert.deepEqual(
    requests.map((request) => request.path),
    [
      '/light-media/api/auth/status',
      '/light-media/api/search',
      '/light-media/api/search/resources',
      '/light-media/api/frontend-errors',
    ],
  );
  assert.ok(requests.every((request) => request.options.credentials === 'same-origin'));
  assert.equal(
    image('/fixture.jpg'),
    '/light-media/api/catalog/image?path=%2Ffixture.jpg&size=w342',
  );
  assert.equal(apiURL('/logs/stream?module=http'), '/light-media/api/logs/stream?module=http');
  assert.equal(apiURL('/logs/export?level=ERROR'), '/light-media/api/logs/export?level=ERROR');
  assert.equal(apiURL('/backups/fixture.db'), '/light-media/api/backups/fixture.db');
  globalThis.location.pathname = '/';
  assert.equal(apiURL('/auth/status'), '/api/auth/status');
});
