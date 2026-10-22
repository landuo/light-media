import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  createSystemMessageFeed,
  parseSystemMessage,
  systemMessageIcon,
  systemMessageRelativeTime,
  SYSTEM_MESSAGE_INITIAL_DELAY,
  SYSTEM_MESSAGE_BACKGROUND_DELAY,
  SYSTEM_MESSAGE_RECONNECT_DELAYS,
  SYSTEM_MESSAGE_FRAME_LIMIT,
} from '../src/features/system-messages.ts';

const body = (title = '站点索引发生了错误', extra = {}) =>
  JSON.stringify({
    type: 'system',
    title,
    text: 'site_user_source_module_failed',
    date: '2026-10-22 14:00:00',
    note: null,
    ...extra,
  });
function fixture() {
  let at = 0,
    nextTimer = 0,
    state = { items: [], hasNew: false };
  const timers = new Map(),
    sources = [];
  function target() {
    const listeners = new Map();
    return {
      hidden: false,
      addEventListener(name, fn) {
        const values = listeners.get(name) || new Set();
        values.add(fn);
        listeners.set(name, values);
      },
      removeEventListener(name, fn) {
        listeners.get(name)?.delete(fn);
      },
      emit(name) {
        for (const fn of [...(listeners.get(name) || [])]) fn();
      },
      count(name) {
        return listeners.get(name)?.size || 0;
      },
    };
  }
  const document = target(),
    window = target();
  const environment = {
    document,
    window,
    setTimeout(callback, delay) {
      const id = ++nextTimer;
      timers.set(id, { at: at + delay, callback });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    createSource(url) {
      const source = {
        url,
        readyState: 0,
        onopen: null,
        onerror: null,
        onmessage: null,
        closes: 0,
        close() {
          this.closes++;
          this.readyState = 2;
        },
        open() {
          this.readyState = 1;
          this.onopen?.();
        },
        error(readyState = 2) {
          this.readyState = readyState;
          this.onerror?.();
        },
        message(data) {
          this.onmessage?.({ data });
        },
      };
      sources.push(source);
      return source;
    },
  };
  const feed = createSystemMessageFeed((next) => {
    state = next;
  }, environment);
  function advance(duration) {
    const until = at + duration;
    while (true) {
      const next = [...timers].sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
      if (!next || next[1].at > until) break;
      at = next[1].at;
      timers.delete(next[0]);
      next[1].callback();
    }
    at = until;
  }
  function hidden(value) {
    document.hidden = value;
    document.emit('visibilitychange');
  }
  return {
    feed,
    environment,
    sources,
    timers,
    document,
    window,
    advance,
    hidden,
    state: () => state,
  };
}

test('frame guard rejects malformed/oversized/unsafe shapes and never retains note', () => {
  for (const value of [
    undefined,
    null,
    '',
    ' ',
    '{',
    'null',
    '[]',
    '0',
    '{}',
    body('ok', { type: {} }),
    body([]),
    body('ok', { text: {} }),
    body('ok', { date: [] }),
    body('x'.repeat(SYSTEM_MESSAGE_FRAME_LIMIT)),
    body('中'.repeat(SYSTEM_MESSAGE_FRAME_LIMIT / 2)),
  ]) {
    assert.equal(parseSystemMessage(value), null);
  }
  assert.deepEqual(
    parseSystemMessage(
      body(null, {
        text: '<img src=x onerror=alert(1)>\n<script>attack()</script>',
        note: { html: '<script>attack()</script>', url: 'javascript:attack()' },
        id: 9,
        read: true,
      }),
    ),
    {
      type: 'system',
      title: null,
      text: '<img src=x onerror=alert(1)>\n<script>attack()</script>',
      date: '2026-10-22 14:00:00',
    },
  );
  assert.equal(systemMessageIcon('user'), 'mdi-account-alert');
  assert.equal(systemMessageIcon('plugin'), 'mdi-robot');
  assert.equal(systemMessageIcon('https://attacker.invalid/icon.svg'), 'mdi-laptop');
});
test('production delays match 3s initial, 5s background and 3s/4.5s/6.75s manual retries', () => {
  assert.equal(SYSTEM_MESSAGE_INITIAL_DELAY, 3000);
  assert.equal(SYSTEM_MESSAGE_BACKGROUND_DELAY, 5000);
  assert.deepEqual(SYSTEM_MESSAGE_RECONNECT_DELAYS, [3000, 4500, 6750]);
});
test('authentication starts one delayed source, never a competing consumer', () => {
  const f = fixture();
  f.advance(10000);
  assert.equal(f.sources.length, 0);
  f.feed.setAuthenticated(true);
  f.feed.setAuthenticated(true);
  assert.equal(f.document.count('visibilitychange'), 1);
  f.advance(2999);
  assert.equal(f.sources.length, 0);
  f.advance(1);
  assert.equal(f.sources.length, 1);
  assert.equal(f.sources[0].url, '/api/system/message');
  f.feed.setAuthenticated(true);
  f.advance(60000);
  assert.equal(f.sources.length, 1);
  f.feed.destroy();
});
test('same-origin cookie source respects HTTPS mount without query credentials', (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'location');
  t.after(() => {
    if (previous) Object.defineProperty(globalThis, 'location', previous);
    else delete globalThis.location;
  });
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: {
      pathname: '/light-media/',
      origin: 'https://synthetic.invalid',
    },
  });
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  assert.equal(f.sources[0].url, '/light-media/api/system/message');
  assert.equal(f.sources[0].url.includes('?'), false);
  f.feed.destroy();
});
test('only 100 newest items live in memory; mark all read is local and does not delete', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  const source = f.sources[0];
  source.message('');
  source.message('malformed');
  assert.equal(f.state().items.length, 0);
  for (let i = 0; i < 105; i++) source.message(body(String(i)));
  assert.equal(f.state().items.length, 100);
  assert.equal(f.state().items[0].title, '104');
  assert.equal(f.state().items[99].title, '5');
  assert.equal(f.state().hasNew, true);
  f.feed.markAllRead();
  assert.equal(f.state().hasNew, false);
  assert.equal(f.state().items.length, 100);
  assert.equal(
    f.state().items.every((x) => x.read),
    true,
  );
  assert.equal(f.sources.length, 1);
  source.message(body('104')); // The original UI does not independently deduplicate.
  assert.equal(f.state().items[0].title, f.state().items[1].title);
  assert.notEqual(f.state().items[0].id, f.state().items[1].id);
  assert.equal(f.state().hasNew, true);
  f.feed.destroy();
});
test('logout during initial delay clears timers and a new login receives a new delay', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(2900);
  f.feed.setAuthenticated(false);
  f.advance(10000);
  assert.equal(f.sources.length, 0);
  assert.equal(f.timers.size, 0);
  assert.equal(f.document.count('visibilitychange'), 0);
  assert.equal(f.window.count('beforeunload'), 0);
  f.feed.setAuthenticated(true);
  f.advance(2999);
  assert.equal(f.sources.length, 0);
  f.advance(1);
  assert.equal(f.sources.length, 1);
  f.feed.destroy();
});
test('expired/logout clears memory and late old callbacks cannot enter a new login', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  const old = f.sources[0],
    lateMessage = old.onmessage,
    lateError = old.onerror,
    lateOpen = old.onopen;
  old.message(body('old'));
  f.feed.setAuthenticated(false);
  assert.equal(old.closes, 1);
  assert.deepEqual(f.state(), { items: [], hasNew: false });
  assert.equal(old.onmessage, null);
  f.feed.setAuthenticated(true);
  f.advance(3000);
  lateMessage({ data: body('stale') });
  lateError();
  lateOpen();
  assert.deepEqual(f.state(), { items: [], hasNew: false });
  assert.equal(f.sources.length, 2);
  f.sources[1].message(body('new'));
  assert.equal(f.state().items[0].title, 'new');
  f.feed.destroy();
  assert.equal(f.timers.size, 0);
  assert.equal(f.sources[1].closes, 1);
});
test('CONNECTING errors stay browser-owned and never create a second source', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  f.sources[0].error(0);
  f.advance(60000);
  assert.equal(f.sources.length, 1);
  assert.equal(f.sources[0].closes, 0);
  assert.equal(f.timers.size, 0);
  f.feed.destroy();
});
test('CLOSED uses three retries; onopen and foreground reset the manual retry chain', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  for (const [index, delay] of [3000, 4500, 6750].entries()) {
    f.sources[index].error();
    assert.equal(f.sources[index].closes, 1);
    f.advance(delay - 1);
    assert.equal(f.sources.length, index + 1);
    f.advance(1);
    assert.equal(f.sources.length, index + 2);
  }
  f.sources[3].error();
  f.advance(60000);
  assert.equal(f.sources.length, 4);
  assert.equal(f.timers.size, 0);
  f.hidden(true);
  f.hidden(false);
  assert.equal(f.sources.length, 5);
  f.sources[4].error();
  f.advance(3000);
  assert.equal(f.sources.length, 6);
  f.sources[5].open();
  f.sources[5].error();
  f.advance(2999);
  assert.equal(f.sources.length, 6);
  f.advance(1);
  assert.equal(f.sources.length, 7);
  f.feed.destroy();
});
test('background closes after five seconds; short switches preserve stream; foreground opens once', () => {
  const f = fixture();
  f.feed.setAuthenticated(true);
  f.advance(3000);
  f.sources[0].open();
  f.hidden(true);
  f.advance(4999);
  assert.equal(f.sources[0].closes, 0);
  f.hidden(false);
  f.advance(1);
  assert.equal(f.sources[0].closes, 0);
  f.hidden(true);
  f.advance(5000);
  assert.equal(f.sources[0].closes, 1);
  f.advance(60000);
  assert.equal(f.sources.length, 1);
  f.hidden(false);
  assert.equal(f.sources.length, 2);
  f.hidden(false);
  assert.equal(f.sources.length, 2);
  f.feed.destroy();
});
test('hidden startup waits for foreground and never bypasses initial three seconds', () => {
  const f = fixture();
  f.document.hidden = true;
  f.feed.setAuthenticated(true);
  f.advance(2000);
  f.hidden(false);
  assert.equal(f.sources.length, 0);
  f.advance(1000);
  assert.equal(f.sources.length, 1);
  f.feed.destroy();
  const g = fixture();
  g.document.hidden = true;
  g.feed.setAuthenticated(true);
  g.advance(10000);
  assert.equal(g.sources.length, 0);
  g.hidden(false);
  assert.equal(g.sources.length, 1);
  g.feed.destroy();
});
test('background cancels retries; logout/unmount/beforeunload clean all timers and listeners', () => {
  for (const finish of ['logout', 'destroy', 'beforeunload']) {
    const f = fixture();
    f.feed.setAuthenticated(true);
    f.advance(3000);
    f.sources[0].error();
    f.hidden(true);
    f.advance(10000);
    assert.equal(f.sources.length, 1);
    f.hidden(false);
    assert.equal(f.sources.length, 2);
    f.sources[1].error();
    if (finish === 'logout') f.feed.setAuthenticated(false);
    if (finish === 'destroy') f.feed.destroy();
    if (finish === 'beforeunload') f.window.emit('beforeunload');
    f.advance(60000);
    assert.equal(f.sources.length, 2, finish);
    assert.equal(f.timers.size, 0, finish);
    assert.equal(f.document.count('visibilitychange'), 0, finish);
    assert.equal(f.window.count('beforeunload'), 0, finish);
    if (finish !== 'logout') {
      f.feed.setAuthenticated(true);
      f.advance(3000);
      assert.equal(f.sources.length, 2);
    }
    f.feed.destroy();
  }
});
// Fixed expectations are read from the pinned Day.js 1.11.13 relativeTime
// thresholds and zh-cn strings, not generated by Day.js or this formatter.
// Calendar cases additionally follow src/utils.js monthDiff and src/index.js
// diff (day offsets are DST-adjusted; hours are elapsed time).
function relativeTimeTimezone(t, zone) {
  const previous = process.env.TZ;
  process.env.TZ = zone;
  t.after(() => {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  });
}
function relativeTimePair(from, to, future, past, name = '') {
  assert.equal(systemMessageRelativeTime(to, Date.parse(from)), future, name + ': future');
  assert.equal(systemMessageRelativeTime(from, Date.parse(to)), past, name + ': past');
}

test('relative time retains original zh-cn wording and the explicit invalid-date display guard', (t) => {
  relativeTimeTimezone(t, 'UTC');
  const now = Date.parse('2026-10-22T14:00:00Z');
  assert.equal(systemMessageRelativeTime(null, now), '');
  assert.equal(systemMessageRelativeTime('', now), '');
  assert.equal(systemMessageRelativeTime('2026-10-22 13:59:00', now), '1 分钟前');
  assert.equal(systemMessageRelativeTime('2026-10-22 14:00:03', now), '几秒内');
  assert.equal(systemMessageRelativeTime('2026-10-22T14:00:00Z', now), '几秒前');
  // Explicit local display hardening, not a claim of malformed-input parity.
  // The upstream formatter has no isValid guard; its invalid output is not
  // established by this test and must be reported separately if investigated.
  assert.equal(systemMessageRelativeTime('invalid', now), '');
  assert.equal(systemMessageRelativeTime('not-a-date', now), '');
  // Day.js parses this non-strict local date through the native Date rollover.
  assert.equal(
    systemMessageRelativeTime('2026-02-30', Date.parse('2026-03-02T00:00:00Z')),
    '几秒前',
  );
});

test('relative time uses original second/minute/hour/day thresholds in both directions', (t) => {
  relativeTimeTimezone(t, 'UTC');
  const now = Date.parse('2026-06-01T00:00:00Z');
  const cases = [
    [3_000, '几秒内', '几秒前'],
    [44_000, '几秒内', '几秒前'],
    [44_499, '几秒内', '几秒前'],
    [44_500, '1 分钟内', '1 分钟前'],
    [45_000, '1 分钟内', '1 分钟前'],
    [89_000, '1 分钟内', '1 分钟前'],
    [89_500, '1 分钟内', '1 分钟前'],
    [89_999, '1 分钟内', '1 分钟前'],
    [90_000, '2 分钟内', '2 分钟前'],
    [2_669_999, '44 分钟内', '44 分钟前'],
    [2_670_000, '1 小时内', '1 小时前'],
    [5_399_999, '1 小时内', '1 小时前'],
    [5_400_000, '2 小时内', '2 小时前'],
    [77_399_999, '21 小时内', '21 小时前'],
    [77_400_000, '1 天内', '1 天前'],
    [129_599_999, '1 天内', '1 天前'],
    [129_600_000, '2 天内', '2 天前'],
    [2_203_199_999, '25 天内', '25 天前'],
    [2_203_200_000, '1 个月内', '1 个月前'],
  ];
  for (const [milliseconds, future, past] of cases) {
    assert.equal(
      systemMessageRelativeTime(new Date(now + milliseconds).toISOString(), now),
      future,
      '+' + milliseconds,
    );
    assert.equal(
      systemMessageRelativeTime(new Date(now - milliseconds).toISOString(), now),
      past,
      '-' + milliseconds,
    );
  }
});

test('relative time month/year thresholds use calendar differences rather than fixed 30/365-day divisors', (t) => {
  relativeTimeTimezone(t, 'UTC');
  const from = '2026-01-01T00:00:00Z';
  const cases = [
    ['2026-02-15T11:59:59.999Z', '1 个月内', '1 个月前'],
    // At 45.5 days, M's day threshold is exceeded but the calendar-month
    // value still rounds to 1, selecting the previous M label again.
    ['2026-02-15T12:00:00Z', '1 个月内', '1 个月前'],
    ['2026-02-16T11:59:59.999Z', '1 个月内', '1 个月前'],
    ['2026-02-16T12:00:00Z', '2 个月内', '2 个月前'],
    ['2026-11-16T11:59:59.999Z', '10 个月内', '10 个月前'],
    ['2026-11-16T12:00:00Z', '1 年内', '1 年前'],
    // Crossing 17.5 months still rounds to one year and uses the y fallback.
    ['2027-06-16T11:59:59.999Z', '1 年内', '1 年前'],
    ['2027-06-16T12:00:00Z', '1 年内', '1 年前'],
    ['2027-06-30T23:59:59.999Z', '1 年内', '1 年前'],
    ['2027-07-01T00:00:00Z', '2 年内', '2 年前'],
    ['2028-01-01T00:00:00Z', '2 年内', '2 年前'],
  ];
  for (const [to, future, past] of cases) relativeTimePair(from, to, future, past, to);
});

test('relative time preserves month-end anchors and leap-year calendar boundaries', (t) => {
  relativeTimeTimezone(t, 'UTC');
  const cases = [
    ['2026-01-31T00:00:00Z', '2026-02-28T00:00:00Z', '1 个月内', '1 个月前'],
    ['2026-01-31T00:00:00Z', '2026-03-31T00:00:00Z', '2 个月内', '2 个月前'],
    ['2026-01-31T00:00:00Z', '2026-12-15T00:00:00Z', '10 个月内', '10 个月前'],
    ['2026-01-31T00:00:00Z', '2026-12-16T00:00:00Z', '1 年内', '1 年前'],
    ['2024-02-29T00:00:00Z', '2025-02-28T00:00:00Z', '1 年内', '1 年前'],
    ['2024-02-29T00:00:00Z', '2025-08-28T23:59:59.999Z', '1 年内', '1 年前'],
    ['2024-02-29T00:00:00Z', '2025-08-29T00:00:00Z', '2 年内', '2 年前'],
  ];
  for (const [from, to, future, past] of cases)
    relativeTimePair(from, to, future, past, from + ' / ' + to);
});

test('relative time respects explicit offsets and parses source dates in the current local timezone', (t) => {
  relativeTimeTimezone(t, 'Asia/Shanghai');
  const now = Date.parse('2026-10-22T14:00:00Z');
  assert.equal(systemMessageRelativeTime('2026-10-22T22:00:00+08:00', now), '几秒前');
  assert.equal(systemMessageRelativeTime('2026-10-22T22:00:03+08:00', now), '几秒内');
  assert.equal(systemMessageRelativeTime('2026-10-22T06:00:03-08:00', now), '几秒内');
  assert.equal(systemMessageRelativeTime('2026-10-22 22:00:03', now), '几秒内');
  process.env.TZ = 'UTC';
  assert.equal(systemMessageRelativeTime('2026-10-22T22:00:03+08:00', now), '几秒内');
  assert.equal(systemMessageRelativeTime('2026-10-22 22:00:03', now), '8 小时内');
});

test('relative time spring DST uses elapsed hours but calendar-adjusted day thresholds', (t) => {
  relativeTimeTimezone(t, 'America/New_York');
  relativeTimePair(
    '2026-03-08T01:00:00-05:00',
    '2026-03-08T03:00:00-04:00',
    '1 小时内',
    '1 小时前',
  );
  relativeTimePair('2026-03-07T12:00:00-05:00', '2026-03-08T12:00:00-04:00', '1 天内', '1 天前');
  const from = '2026-03-01T12:00:00-05:00';
  relativeTimePair(from, '2026-03-26T23:59:59.999-04:00', '25 天内', '25 天前');
  relativeTimePair(from, '2026-03-27T00:00:00-04:00', '1 个月内', '1 个月前');
  // The source parser normalizes the nonexistent 02:30 local time to 03:30.
  assert.equal(
    systemMessageRelativeTime('2026-03-08 02:30:00', Date.parse('2026-03-08T03:30:00-04:00')),
    '几秒前',
  );
});

test('relative time autumn DST distinguishes the repeated hour and preserves day rounding', (t) => {
  relativeTimeTimezone(t, 'America/New_York');
  relativeTimePair(
    '2026-11-01T01:30:00-04:00',
    '2026-11-01T01:30:00-05:00',
    '1 小时内',
    '1 小时前',
  );
  relativeTimePair('2026-10-31T12:00:00-04:00', '2026-11-01T12:00:00-05:00', '1 天内', '1 天前');
  const from = '2026-10-15T12:00:00-04:00';
  relativeTimePair(from, '2026-11-09T23:59:59.999-05:00', '25 天内', '25 天前');
  relativeTimePair(from, '2026-11-10T00:00:00-05:00', '1 个月内', '1 个月前');
  // For the ambiguous local time, Date/Day.js choose the earlier occurrence.
  assert.equal(
    systemMessageRelativeTime('2026-11-01 01:30:00', Date.parse('2026-11-01T01:30:00-04:00')),
    '几秒前',
  );
});
test('App uses one bell, text-only rendering, separate durable actions and explicit auth cleanup', () => {
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8');
  const helper = readFileSync(
    new URL('../src/features/system-messages.ts', import.meta.url),
    'utf8',
  );
  const css = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8');
  const system = source.slice(
    source.indexOf('id="system-message-list"'),
    source.indexOf('id="task-notification-list"'),
  );
  assert.match(system, /{{ n\.title }}/);
  assert.match(system, /{{ n\.text }}/);
  assert.match(system, /systemMessageIcon\(n\.type\)/);
  assert.match(system, /systemMessageRelativeTime\(n\.date, notificationTime\)/);
  assert.doesNotMatch(system, /v-html|innerHTML|\.note|readNotification\(/);
  assert.match(source, /api\('\/notifications\/'.*'\/read'/);
  assert.match(
    source,
    /function readAllSystemMessages\(\) \{\s*systemMessageFeed\.markAllRead\(\);\s*notificationsOpen\.value = false;/,
  );
  assert.match(
    source,
    /function expired\(\) \{[\s\S]*?systemMessageFeed\.setAuthenticated\(false\)/,
  );
  assert.match(source, /async function logout\(\) \{[\s\S]*?expired\(\);\s*await action/);
  assert.match(source, /onUnmounted\(\(\) => \{[\s\S]*?systemMessageFeed\.destroy\(\)/);
  assert.match(source, /window\.addEventListener\('session-expired', expired\)/);
  assert.match(helper, /new EventSource\(url\)/);
  assert.doesNotMatch(helper, /localStorage|sessionStorage|Authorization|access_token|\?token=/);
  assert.match(css, /\.notification-panel \{[\s\S]*?width: 400px/);
  assert.match(css, /\.notification-list-container \{\s*max-height: 50vh/);
  assert.equal((source.match(/class="bell-button"/g) || []).length, 1);
});

test('actual App system-message markup renders HTML/script payloads as escaped text, never note', async () => {
  const Vue = await import('vue');
  const { compile } = await import('@vue/compiler-dom');
  const { renderToString } = await import('@vue/server-renderer');
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8');
  const start = source.indexOf('<article v-for="n in systemMessages.items"');
  // Prettier may wrap the opening tag, so find the owning article from v-for.
  const marker = source.indexOf('v-for="n in systemMessages.items"');
  const articleStart = start >= 0 ? start : source.lastIndexOf('<article', marker);
  const articleEnd = source.indexOf('</article>', marker) + '</article>'.length;
  assert.ok(marker >= 0 && articleStart >= 0 && articleEnd > articleStart);
  const render = new Function(
    'Vue',
    compile(source.slice(articleStart, articleEnd), { mode: 'function' }).code,
  )(Vue);
  const text = '<script>globalThis.syntheticAttack = true</script> & <img src=x onerror=attack()> ';
  const application = Vue.createSSRApp({
    setup: () => ({
      systemMessages: {
        items: [
          {
            id: 1,
            type: 'system',
            title: text,
            text,
            date: null,
            note: 'NEVER_RENDER_NOTE_SENTINEL',
          },
        ],
      },
      notificationTime: Date.now(),
      notificationsOpen: true,
      systemMessageIcon,
      systemMessageRelativeTime,
    }),
    render,
  });
  application.component('VIcon', { render: () => Vue.h('span', { 'data-icon-stub': '' }) });
  const output = await renderToString(application);
  assert.match(output, /&lt;script&gt;globalThis\.syntheticAttack = true&lt;\/script&gt;/);
  assert.match(output, /&lt;img src=x onerror=attack\(\)&gt;/);
  assert.doesNotMatch(output, /<script>|<img |NEVER_RENDER_NOTE_SENTINEL/);
  assert.equal(globalThis.syntheticAttack, undefined);
});

test('actual App rendering reads the current clock after login/background while keeping the 15-second trigger', async (t) => {
  relativeTimeTimezone(t, 'UTC');
  const Vue = await import('vue');
  const { compile } = await import('@vue/compiler-dom');
  const { parse } = await import('@vue/compiler-sfc');
  const { renderToString } = await import('@vue/server-renderer');
  const { default: ts } = await import('typescript');
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8');
  const script = parse(source).descriptor.scriptSetup.content;
  const ast = ts.createSourceFile('App.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const formatter = ast.statements.find(
    (statement) => ts.isFunctionDeclaration(statement) && statement.name?.text === 'systemMessageRelativeTime',
  );
  assert.ok(formatter, 'exercise the actual App-local formatter, not a test replacement');
  assert.match(script, /systemMessageRelativeTime as formatSystemMessageRelativeTime/);
  const appRelativeTime = new Function(
    'formatSystemMessageRelativeTime',
    ts.transpileModule(formatter.getText(ast), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText +
      '\nreturn systemMessageRelativeTime;',
  )(systemMessageRelativeTime);
  const marker = source.indexOf('v-for="n in systemMessages.items"');
  const articleStart = source.lastIndexOf('<article', marker);
  const articleEnd = source.indexOf('</article>', marker) + '</article>'.length;
  assert.ok(marker >= 0 && articleStart >= 0 && articleEnd > articleStart);
  const render = new Function(
    'Vue',
    compile(source.slice(articleStart, articleEnd), { mode: 'function' }).code,
  )(Vue);
  const staleTick = Date.parse('2026-10-22T13:00:00Z');
  let wallClock = Date.parse('2026-10-22T14:00:00Z');
  t.mock.method(Date, 'now', () => wallClock);
  const items = [
    { id: 1, type: 'system', title: 'new after login or resume', text: '', date: '2026-10-22T13:59:57Z' },
    { id: 2, type: 'system', title: 'retained while in background', text: '', date: '2026-10-22T12:59:00Z' },
  ];
  async function renderWith(format) {
    const application = Vue.createSSRApp({
      setup: () => ({
        systemMessages: { items },
        notificationTime: staleTick,
        notificationsOpen: true,
        systemMessageIcon,
        systemMessageRelativeTime: format,
      }),
      render,
    });
    application.component('VIcon', { render: () => Vue.h('span', { 'data-icon-stub': '' }) });
    return renderToString(application);
  }
  // The actual unchanged template with the old formatter proves the regression:
  // neither arrival, opening the bell, nor this test updates the stale tick.
  const oldOutput = await renderWith(systemMessageRelativeTime);
  assert.match(oldOutput, /<small>1 小时内<\/small>/);
  assert.match(oldOutput, /<small>1 分钟前<\/small>/);
  const output = await renderWith(appRelativeTime);
  assert.match(output, /<small>几秒前<\/small>/);
  assert.match(output, /<small>1 小时前<\/small>/);
  assert.doesNotMatch(output, /<small>1 小时内<\/small>|<small>1 分钟前<\/small>/);
  wallClock = Date.parse('2026-10-22T14:02:57Z');
  assert.match(await renderWith(appRelativeTime), /<small>3 分钟前<\/small>/);
  assert.equal(appRelativeTime(items[0].date, 0), '3 分钟前', 'zero tick is also only a trigger');

  // Reading the existing reactive argument still invalidates a render on tick;
  // moving the wall clock alone does not create a new timer or listener.
  const tick = Vue.ref(staleTick);
  let renders = 0, displayed;
  const effect = Vue.effect(() => {
    renders++;
    displayed = appRelativeTime(items[0].date, tick.value);
  });
  t.after(() => Vue.stop(effect));
  assert.equal(displayed, '3 分钟前');
  wallClock += 60_000;
  assert.equal(displayed, '3 分钟前');
  assert.equal(renders, 1);
  tick.value = wallClock;
  assert.equal(displayed, '4 分钟前');
  assert.equal(renders, 2);
  assert.match(script, /if \(auth\.value\?\.authenticated && !document\.hidden\) \{\s*notificationTime\.value = Date\.now\(\);/);
  assert.match(script, /\}, 15000\);/);
});
