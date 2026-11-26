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
  assert.ok(captured.options.signal instanceof AbortSignal);
  assert.equal(captured.options.signal.aborted, false);
  assert.equal(captured.options.credentials, 'same-origin');
  assert.equal(captured.options.headers.Accept, 'text/event-stream');
  assert.equal(JSON.parse(captured.options.body).tmdb_id, undefined);
});
test('keyword resources publish each completed site before the final snapshot', async (t) => {
  const old = globalThis.fetch;
  const progress = [];
  const first = {
    candidates: [{ resource: { id: 'fast' } }],
    sites: [
      { id: 'fast', status: 'ok' },
      { id: 'slow', status: 'searching' },
    ],
  };
  const final = {
    candidates: [...first.candidates, { resource: { id: 'slow' } }],
    sites: [
      { id: 'fast', status: 'ok' },
      { id: 'slow', status: 'ok' },
    ],
  };
  globalThis.fetch = async () =>
    new Response(
      `event: progress\ndata: ${JSON.stringify(first)}\n\nevent: complete\ndata: ${JSON.stringify(final)}\n\n`,
      { headers: { 'Content-Type': 'text/event-stream' } },
    );
  t.after(() => {
    globalThis.fetch = old;
    assert.equal(ui.busy, 0);
  });
  assert.deepEqual(
    await keywordResourceSearch({ query: 'synthetic' }, new AbortController().signal, (value) =>
      progress.push(value),
    ),
    final,
  );
  assert.deepEqual(progress, [first, final]);
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

test('browser keyword deadline preserves complete frames even when the transport never closes', async (t) => {
  for (const scenario of ['timeout', 'unconfirmed filter', 'no frame', 'cancel', 'complete']) {
    await t.test(scenario, async (t) => {
      t.mock.timers.enable({ apis: ['setTimeout'] });
      const original = globalThis.fetch;
      const user = new AbortController();
      const opened = Promise.withResolvers();
      const published = Promise.withResolvers();
      const snapshots = [];
      let connection, network;
      const initial = {
        sites: [{ id: 'fast', status: 'searching', count: 2 }],
        candidates:
          scenario === 'unconfirmed filter' ? [] : [{ resource: { site_id: 'fast', id: '1' } }],
      };
      const send = (event, snapshot) =>
        connection.enqueue(
          new TextEncoder().encode(`event: ${event}\ndata: ${JSON.stringify(snapshot)}\n\n`),
        );
      globalThis.fetch = async (_url, options) => {
        network = options.signal;
        const body = new ReadableStream({
          start(controller) {
            connection = controller;
            if (scenario !== 'no frame') send('progress', initial);
          },
          // A delayed TCP/proxy close must not hold the screen in loading state.
          cancel() {
            return new Promise(() => {});
          },
        });
        opened.resolve();
        return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } });
      };
      t.after(() => {
        globalThis.fetch = original;
      });
      const search = keywordResourceSearch({ query: 'synthetic' }, user.signal, (frame) => {
        snapshots.push(frame);
        published.resolve();
      });
      await (scenario === 'no frame' ? opened.promise : published.promise);
      t.mock.timers.tick(14999);
      assert.equal(network.aborted, false);
      if (scenario === 'complete') {
        const final = { ...initial, sites: [{ id: 'fast', status: 'ok', count: 1 }] };
        send('complete', final);
        assert.deepEqual(await search, final);
        t.mock.timers.tick(30000);
        assert.equal(network.aborted, false, 'the real final frame cleared its timer');
        assert.equal(snapshots.length, 2);
      } else if (scenario === 'cancel') {
        user.abort();
        await assert.rejects(search, (error) => error.name === 'AbortError');
        t.mock.timers.tick(1);
        assert.equal(network.reason, user.signal.reason);
        assert.deepEqual(snapshots, [initial]);
      } else {
        t.mock.timers.tick(1);
        await assert.rejects(search, (error) => {
          assert.equal(error.code, 'search_wait_timeout');
          assert.match(
            error.message,
            scenario === 'timeout' ? /已保留 1 个资源/ : /尚未收到可展示的结果/,
          );
          return true;
        });
        assert.equal(user.signal.aborted, false);
        assert.deepEqual(snapshots, scenario === 'no frame' ? [] : [initial]);
      }
      assert.equal(ui.busy, 0);
    });
  }
});
