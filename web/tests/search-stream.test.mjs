import test from 'node:test';
import assert from 'node:assert/strict';
import { searchStream, APIError, ui } from '../src/api.ts';

function useFetch(t, fn) {
  const original = globalThis.fetch;
  globalThis.fetch = fn;
  t.after(() => {
    globalThis.fetch = original;
    assert.equal(ui.busy, 0);
  });
}
function eventResponse(text, split = 1) {
  const bytes = new TextEncoder().encode(text);
  let offset = 0;
  return new Response(
    new ReadableStream({
      pull(c) {
        if (offset === bytes.length) {
          c.close();
          return;
        }
        c.enqueue(bytes.slice(offset, offset + split));
        offset = Math.min(bytes.length, offset + split);
      },
    }),
    { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': 'req-unit' } },
  );
}

test('streams cumulative progress across split UTF-8 and CRLF frames', async (t) => {
  const progress = [];
  let request;
  const first = {
      workflow_id: 'wf-unit',
      candidates: [],
      sites: [{ id: 'synthetic', status: 'ok', message: '中文测试', count: 0 }],
    },
    final = { ...first, candidates: [{ resource: { id: '1', title: '合成单元测试' } }] };
  useFetch(t, async (path, options) => {
    request = { path, options };
    return eventResponse(
      `: heartbeat\r\n\r\nevent: progress\r\ndata: ${JSON.stringify(first)}\r\n\r\nevent: complete\r\ndata: ${JSON.stringify(final)}\r\n\r\n`,
      1,
    );
  });
  const signal = new AbortController().signal,
    out = await searchStream({ page: 2 }, (value) => progress.push(value), signal);
  assert.deepEqual(progress, [first, final]);
  assert.deepEqual(out, final);
  assert.equal(request.path, '/api/search');
  assert.equal(request.options.signal, signal);
  assert.equal(request.options.credentials, 'same-origin');
  assert.equal(request.options.headers.Accept, 'text/event-stream');
  assert.equal(request.options.headers['X-Light-Media-Search-Stream'], undefined);
  assert.equal(JSON.parse(request.options.body).page, 2);
});

test('a stream error retains completed progress and fails the request', async (t) => {
  const progress = [];
  useFetch(t, async () =>
    eventResponse(
      'event: progress\ndata: {"candidates":[],"sites":[{"id":"synthetic","status":"ok"}]}\n\nevent: error\ndata: {"error":{"code":"SITE_FAILED","message":"site_transport_error"},"request_id":"req-error"}\n\n',
      9,
    ),
  );
  await assert.rejects(
    searchStream({}, (p) => progress.push(p), new AbortController().signal),
    (e) =>
      e instanceof APIError &&
      e.code === 'SITE_FAILED' &&
      e.requestId === 'req-error' &&
      e.message === '无法连接站点',
  );
  assert.equal(progress.length, 1);
});

test('an incomplete or malformed stream never reports a fabricated success', async (t) => {
  useFetch(t, async () =>
    eventResponse('event: progress\ndata: {"candidates":[],"sites":[]}\n\n', 5),
  );
  await assert.rejects(
    searchStream({}, () => {}, new AbortController().signal),
    (e) => e.code === 'stream_interrupted',
  );
  globalThis.fetch = async () =>
    eventResponse('event: complete\ndata: synthetic-private-response-text\n\n', 7);
  await assert.rejects(
    searchStream({}, () => {}, new AbortController().signal),
    (e) => e.code === 'invalid_response' && !e.message.includes('private-response'),
  );
});

test('ordinary JSON response remains compatible and reports real API errors', async (t) => {
  const value = { candidates: [], sites: [] },
    progress = [];
  useFetch(
    t,
    async () =>
      new Response(JSON.stringify({ data: value, request_id: 'req-json' }), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  assert.deepEqual(
    await searchStream({}, (v) => progress.push(v), new AbortController().signal),
    value,
  );
  assert.deepEqual(progress, [value]);
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        error: { code: 'NO_SITES', message: '请配置站点' },
        request_id: 'req-failed',
      }),
      { status: 400, headers: { 'Content-Type': 'application/json' } },
    );
  await assert.rejects(
    searchStream({}, () => {}, new AbortController().signal),
    (e) => e.code === 'NO_SITES' && e.requestId === 'req-failed',
  );
});

test('cancellation reaches the actual fetch signal and releases busy state', async (t) => {
  let aborted = false;
  useFetch(
    t,
    async (_path, options) =>
      new Promise((_resolve, reject) =>
        options.signal.addEventListener(
          'abort',
          () => {
            aborted = true;
            reject(new DOMException('aborted', 'AbortError'));
          },
          { once: true },
        ),
      ),
  );
  const controller = new AbortController(),
    request = searchStream({}, () => assert.fail('no progress expected'), controller.signal);
  controller.abort();
  await assert.rejects(request, (e) => e.name === 'AbortError');
  assert.equal(aborted, true);
});

function keywordDelta(event, sequence, fields) {
  const payload = {
    sequence,
    workflow_id: 'wf-search',
    cached_at: '2026-11-26T12:30:00Z',
    sites: [{ id: 'one', status: 'searching' }],
    ...fields,
  };
  return `event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`;
}

test('incremental keyword frames preserve candidates and apply the server order', async (t) => {
  const first = { resource: { id: 'a', site_id: 'one' }, accepted: true };
  const second = { resource: { id: 'b', site_id: 'two' }, accepted: true };
  const changed = { ...first, accepted: false };
  const arrival = keywordDelta('progress', 1, { candidates_added: [first], candidate_order: [0] });
  const statusOnly = keywordDelta('progress', 2, {});
  const ranked = keywordDelta('progress', 3, {
    candidates_added: [second],
    candidate_order: [1, 0],
  });
  const final = keywordDelta('complete', 4, {
    sites: [
      { id: 'one', status: 'ok' },
      { id: 'two', status: 'ok' },
    ],
    candidates_added: [changed],
    candidate_order: [2, 1],
  });
  useFetch(t, async (_url, options) => {
    assert.equal(options.headers['X-Light-Media-Search-Stream'], 'incremental');
    return eventResponse(arrival + arrival + statusOnly + ranked + statusOnly + final, 111);
  });
  const frames = [];
  const result = await searchStream(
    {},
    (v) => frames.push(v),
    new AbortController().signal,
    '/search/resources',
  );
  assert.equal(frames.length, 4);
  assert.strictEqual(frames[0].candidates, frames[1].candidates);
  assert.strictEqual(frames[2].candidates[1], frames[0].candidates[0]);
  assert.strictEqual(result.candidates[1], frames[2].candidates[0]);
  assert.strictEqual(result, frames[3]);
  assert.deepEqual(result.candidates, [changed, second]);
  assert.deepEqual(frames[0].candidates, [first]);
  assert.deepEqual(Object.keys(result).sort(), ['cached_at', 'candidates', 'sites', 'workflow_id']);
});

test('keyword stream interruptions keep the last complete candidate snapshot', async (t) => {
  const first = { resource: { id: 'a' } };
  const arrival = keywordDelta('progress', 1, { candidates_added: [first], candidate_order: [0] });
  for (const trailer of [
    '',
    keywordDelta('complete', 3, { candidate_order: [0] }),
    keywordDelta('complete', 2, { candidate_order: [7] }),
    keywordDelta('complete', 2, { workflow_id: 'another-search', candidate_order: [0] }),
  ]) {
    await t.test(async (t) => {
      useFetch(t, async () => eventResponse(arrival + trailer, 113));
      const frames = [];
      await assert.rejects(
        searchStream({}, (v) => frames.push(v), new AbortController().signal, '/search/resources'),
        (err) => err.code === 'stream_interrupted',
      );
      assert.deepEqual(
        frames.map((v) => v.candidates),
        [[first]],
      );
    });
  }
});
