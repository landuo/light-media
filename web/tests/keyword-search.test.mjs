import test from 'node:test';
import assert from 'node:assert/strict';
import { keywordResourceSearch, resourceTitleQuery } from '../src/features/keyword-search.ts';
import { ui, APIError } from '../src/api.ts';
test('keyword resource search sends an abortable authenticated request without inventing a TMDB identity', async (t) => {
  const old = globalThis.fetch;
  const control = new AbortController();
  let captured;
  globalThis.fetch = async (path, options) => {
    captured = { path, options };
    return new Response(JSON.stringify({ data: { candidates: [], sites: [] } }), {
      headers: { 'Content-Type': 'application/json' },
    });
  };
  t.after(() => {
    globalThis.fetch = old;
    assert.equal(ui.busy, 0);
  });
  assert.deepEqual(
    await keywordResourceSearch(
      { query: 'synthetic-release', site_ids: ['one'], page: 1 },
      control.signal,
    ),
    { candidates: [], sites: [] },
  );
  assert.equal(captured.path, '/api/search/resources');
  assert.equal(captured.options.signal, control.signal);
  assert.equal(captured.options.credentials, 'same-origin');
  assert.equal(JSON.parse(captured.options.body).tmdb_id, undefined);
});
test('keyword search surfaces actual API failure and releases request state', async (t) => {
  const old = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        error: { code: 'SITE_FAILED', message: '站点无法连接' },
        request_id: 'synthetic-id',
      }),
      { status: 502 },
    );
  t.after(() => {
    globalThis.fetch = old;
    assert.equal(ui.busy, 0);
  });
  await assert.rejects(
    keywordResourceSearch({ query: 'synthetic' }, new AbortController().signal),
    (e) => e instanceof APIError && e.code === 'SITE_FAILED' && e.requestId === 'synthetic-id',
  );
});
test('release title is only a suggested catalog query, never an automatically selected identity', () => {
  assert.equal(
    resourceTitleQuery('Synthetic.Movie.2025.2160p.WEB-DL.HEVC-GROUP'),
    'Synthetic Movie',
  );
  assert.equal(resourceTitleQuery('Synthetic Show S02E05 1080p'), 'Synthetic Show');
  assert.equal(resourceTitleQuery('[GROUP] 2025 1080p', 'fallback title'), 'fallback title');
});
