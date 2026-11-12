import test from 'node:test';
import assert from 'node:assert/strict';
import { boundedRead } from '../src/features/bounded-read.ts';
function waiting(signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  });
}
test('display reads abort on budget and preserve cancellation', async () => {
  await assert.rejects(boundedRead(waiting, new AbortController().signal, 10), /读取超时/);
  const parent = new AbortController();
  const result = boundedRead(waiting, parent.signal, 500);
  parent.abort(new Error('session ended'));
  await assert.rejects(result, /session ended/);
  await assert.rejects(boundedRead(waiting, parent.signal), /session ended/);
});
test('successful display reads return without aborting their parent', async () => {
  const parent = new AbortController();
  assert.equal(await boundedRead(async () => 42, parent.signal), 42);
  assert.equal(parent.signal.aborted, false);
});
