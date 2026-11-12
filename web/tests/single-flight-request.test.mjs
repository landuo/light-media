import test from 'node:test';
import assert from 'node:assert/strict';
import { createSingleFlightRequest } from '../src/features/single-flight-request.ts';

test('a five-second response survives three-second silent polling without duplicate requests', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] });
  let calls = 0;
  const results = [];
  const requests = createSingleFlightRequest({
    request: () => { calls++; return new Promise(resolve => setTimeout(() => resolve('tasks'), 5000)); },
    result: value => results.push(value), error: assert.fail, settled() {},
  });
  const first = requests.run('qb');
  await Promise.resolve();
  const timer = setInterval(() => requests.run('qb'), 3000);
  t.mock.timers.tick(3000);
  assert.equal(calls, 1);
  assert.equal(requests.run('qb'), first);
  t.mock.timers.tick(2000);
  await first;
  assert.deepEqual(results, ['tasks']);
  assert.equal(requests.pending, false);
  clearInterval(timer);
});

test('switch/unmount abort transport and ignore late success, error and settled callbacks', async () => {
  const pending = [], results = [], errors = [];
  let settled = 0;
  const requests = createSingleFlightRequest({
    request: (key, signal) => new Promise((resolve, reject) => pending.push({ key, signal, resolve, reject })),
    result: value => results.push(value), error: value => errors.push(value), settled: () => settled++,
  });
  const first = requests.run('old'); await Promise.resolve();
  requests.cancel(); assert.equal(pending[0].signal.aborted, true);
  const second = requests.run('new'); await Promise.resolve();
  pending[0].resolve('stale'); await first;
  assert.deepEqual(results, []); assert.equal(requests.pending, true); assert.equal(settled, 0);
  pending[1].resolve('current'); await second;
  assert.deepEqual(results, ['current']); assert.equal(settled, 1);
  const third = requests.run('unmounted'); await Promise.resolve();
  requests.cancel(); pending[2].reject(new Error('aborted')); await third;
  assert.deepEqual(errors, []); assert.equal(settled, 1);
});

test('failure releases the flight so the next poll can recover', async () => {
  let calls = 0; const results = [], errors = [];
  const requests = createSingleFlightRequest({
    request: async () => { if (++calls === 1) throw new Error('offline'); return 'recovered'; },
    result: value => results.push(value), error: value => errors.push(value.message), settled() {},
  });
  await requests.run('qb'); await requests.run('qb');
  assert.deepEqual(errors, ['offline']); assert.deepEqual(results, ['recovered']);
});
