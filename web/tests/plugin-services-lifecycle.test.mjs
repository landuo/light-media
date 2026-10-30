import test from 'node:test';
import assert from 'node:assert/strict';
import { servicesFixture, serviceRow } from './helpers/plugin-services-fixture.mjs';

function fixture(t, options) {
  const f = servicesFixture(options);
  t.after(f.stop);
  return f;
}
const reads = (f) => f.requests.filter((request) => request.kind === 'read');

test('services hidden initial mount reads immediately and preserves the initial five-second anchor', async (t) => {
  const f = fixture(t, { initialHidden: true });
  await f.mount();
  assert.equal(reads(f).length, 1);
  const [timerID, timer] = [...f.timers][0];
  assert.equal(timer.ms, 5000);
  await f.advance(5000);
  assert.equal(reads(f).length, 1, 'hidden interval is gated, not the initial read');
  await f.advance(1200);
  await f.hidden(false);
  assert.equal(reads(f).length, 1, 'first visible event does not read');
  assert.equal([...f.timers.keys()][0], timerID);
  await f.advance(3800);
  assert.equal(reads(f).length, 2);
  assert.equal(reads(f)[1].at, 10000);
});

test('services background transition pauses polling and resumes with a fresh interval, not a read', async (t) => {
  const f = fixture(t);
  await f.mount();
  await f.advance(1700);
  await f.hidden(true);
  assert.equal(f.timers.size, 0);
  await f.hidden(true);
  await f.advance(20000);
  assert.equal(reads(f).length, 1);
  await f.hidden(false);
  assert.equal(reads(f).length, 1);
  const timerID = [...f.timers.keys()][0];
  await f.hidden(false);
  assert.equal(
    [...f.timers.keys()][0],
    timerID,
    'duplicate visibility events do not reset the interval',
  );
  await f.advance(4999);
  assert.equal(reads(f).length, 1);
  await f.advance(1);
  assert.equal(reads(f).length, 2);
  f.stop();
  assert.equal(f.timers.size, 0);
  assert.equal(f.listeners.size, 0);
});

test('services initial read must settle before adding its polling timer', async (t) => {
  const f = fixture(t, { initialMode: 'held' });
  await f.mount();
  await f.advance(12000);
  assert.equal(reads(f).length, 1);
  assert.equal(f.timers.size, 0);
  await f.hidden(true);
  f.heldReads[0].resolve([serviceRow('initial')]);
  await f.settle();
  assert.equal([...f.timers.values()][0].due, 17000);
  await f.advance(5000);
  assert.equal(reads(f).length, 1);
  await f.hidden(false);
  assert.equal(reads(f).length, 1);
  f.mode('ok');
  await f.advance(5000);
  assert.equal(reads(f).length, 2);
});

test('services poll is single-flight but manual delayed reads bypass that gate even hidden', async (t) => {
  const f = fixture(t);
  await f.mount();
  f.mode('held');
  await f.advance(15000);
  assert.equal(reads(f).length, 2, 'only one pending poll across three timer ticks');
  f.run();
  await f.hidden(true);
  await f.advance(1000);
  assert.equal(
    reads(f).length,
    3,
    'manual refresh is not dropped behind pending poll or background gate',
  );
  f.heldReads[1].resolve([serviceRow('manual-first')]);
  await f.settle();
  assert.equal(f.state.items.value[0].id, 'manual-first');
  f.heldReads[0].resolve([serviceRow('poll-last')]);
  await f.settle();
  assert.equal(
    f.state.items.value[0].id,
    'poll-last',
    'source applies completion order, not request-generation order',
  );
  f.submissions[0].resolve({ success: true });
  await f.hidden(false);
  assert.equal(reads(f).length, 3);
});

test('services manual refresh may finish before a pending initial read without starting its timer', async (t) => {
  const f = fixture(t, { initialMode: 'held' });
  await f.mount();
  f.run();
  await f.advance(1000);
  assert.equal(f.heldReads.length, 2);
  f.heldReads[1].resolve([serviceRow('manual-first')]);
  await f.settle();
  assert.equal(f.state.items.value[0].id, 'manual-first');
  assert.equal(f.timers.size, 0);
  f.heldReads[0].resolve([serviceRow('initial-last')]);
  await f.settle();
  assert.equal(f.state.items.value[0].id, 'initial-last');
  assert.equal([...f.timers.values()][0].due, 6000);
  f.submissions[0].resolve({ success: true });
});

test('services repeated submissions each acknowledge immediately and independently refresh after one second', async (t) => {
  const f = fixture(t);
  await f.mount();
  f.run();
  f.run();
  assert.equal(f.submissions.length, 2);
  assert.deepEqual(
    f.toasts.map(({ text, error }) => ({ text, error })),
    [
      { text: '定时作业执行请求提交成功！', error: false },
      { text: '定时作业执行请求提交成功！', error: false },
    ],
  );
  await f.advance(999);
  assert.equal(reads(f).length, 1);
  await f.advance(1);
  assert.equal(reads(f).length, 3);
  assert.equal(f.timers.size, 1, 'expired manual timers are removed from cleanup tracking');
  f.submissions.forEach((pending) => pending.resolve({ success: true }));
});

test('services cold failure remains ordinary empty UI, later failure retains rows and polling recovers', async (t) => {
  const f = fixture(t, { initialMode: 'failure' });
  await f.mount();
  assert.deepEqual(f.state.items.value, []);
  assert.equal(f.timers.size, 1);
  assert.doesNotMatch(f.descriptor.template.content, /VAlert|重试|正在加载|服务列表加载失败/);
  assert.match(f.descriptor.template.content, /没有后台服务/);
  f.mode('ok', 'old');
  await f.advance(5000);
  f.mode('failure');
  await f.advance(5000);
  assert.equal(f.state.items.value[0].id, 'old');
  f.mode('ok', 'recovered');
  await f.advance(5000);
  assert.equal(f.state.items.value[0].id, 'recovered');
  assert.equal(f.toasts.length, 0);
  assert.ok(
    !JSON.stringify(f.logs).includes('Owned injected read failure'),
    'do not print potentially sensitive error objects',
  );
});

test('services rejected execution does not replace its immediate acknowledgement with new error UI', async (t) => {
  const f = fixture(t);
  await f.mount();
  f.run(serviceRow('Owned/中文 tick'));
  const request = f.requests.find((entry) => entry.kind === 'run');
  assert.equal(
    request.path,
    '/v1/system/runscheduler?jobid=' + encodeURIComponent('Owned/中文 tick'),
  );
  f.submissions[0].reject(new Error('secret-request-context-must-not-be-logged'));
  await f.settle();
  await f.advance(1000);
  assert.equal(f.toasts.length, 1);
  assert.equal(f.toasts[0].error, false);
  assert.equal(reads(f).length, 2);
  assert.ok(!JSON.stringify(f.logs).includes('secret-request-context'));
});

test('services uses original running-only execution disable and stable original row identity', async (t) => {
  const f = fixture(t);
  await f.mount();
  f.run({ ...serviceRow('running'), status: '正在运行' });
  assert.equal(f.submissions.length, 0);
  f.run({ ...serviceRow('stopped'), status: '停止', can_run: false });
  assert.equal(f.submissions.length, 1, 'can_run is not an extra source UI precondition');
  assert.match(f.descriptor.template.content, /:disabled="item\.status === '正在运行'"/);
  assert.match(f.descriptor.template.content, /:key="item\.id"/);
  assert.doesNotMatch(f.descriptor.scriptSetup.content, /allowRefresh/);
  f.submissions[0].resolve({ success: true });
});

test('services unmount cannot resurrect a timer or apply a late initial response', async (t) => {
  const f = fixture(t, { initialMode: 'held' });
  await f.mount();
  f.stop();
  f.heldReads[0].resolve([serviceRow('late')]);
  await f.settle();
  assert.deepEqual(f.state.items.value, []);
  assert.equal(f.timers.size, 0);
  assert.equal(f.listeners.size, 0);
  await f.advance(20000);
  assert.equal(reads(f).length, 1);
});

test('services unmount cancels pending manual refreshes and does not apply a late poll response', async (t) => {
  const f = fixture(t);
  await f.mount();
  f.mode('held');
  await f.advance(5000);
  f.run();
  f.stop();
  assert.equal(f.timers.size, 0);
  assert.equal(f.listeners.size, 0);
  await f.advance(1000);
  assert.equal(reads(f).length, 2);
  f.heldReads[0].resolve([serviceRow('late-poll')]);
  await f.settle();
  assert.equal(f.state.items.value[0].id, 'initial');
  f.submissions[0].resolve({ success: true });
});
