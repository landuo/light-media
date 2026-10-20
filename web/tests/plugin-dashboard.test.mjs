import test from 'node:test';
import assert from 'node:assert/strict';
import {
  dashboardID,
  dashboardEnabled,
  dashboardPreferredLayout,
  dashboardResponse,
  dashboardProfile,
  dashboardColumns,
  dashboardLayout,
  dashboardWidget,
  dashboardRefresh,
} from '../src/features/plugin-dashboard.ts';
test('installed dashboard breakpoint/ID and exact source width coercion', () => {
  assert.equal(dashboardID({ id: 'SystemProcess', key: 'default' }), 'SystemProcess:default');
  assert.deepEqual([640, 641, 960, 961].map(dashboardProfile), [
    'mobile',
    'tablet',
    'tablet',
    'desktop',
  ]);
  assert.deepEqual(['mobile', 'tablet', 'desktop'].map(dashboardColumns), [1, 6, 12]);
  for (const [raw, w] of [
    ['6', 6],
    [null, 12],
    ['', 1],
    [0, 1],
    [false, 1],
    ['broken', 12],
    [5.5, 6],
  ]) {
    assert.equal(
      dashboardWidget({ id: 'x', key: '', cols: { md: raw } }, 'desktop', { items: {} }).w,
      w,
    );
  }
});
test('installed per-profile layout/visibility normalization retains explicit reset envelope', () => {
  assert.equal(dashboardLayout(null), undefined);
  const saved = dashboardLayout({
    items: { a: { x: 10, y: -2, w: 6, h: 500 }, b: { w: '4' } },
    enabled: { a: 'false', b: 0 },
    updatedAt: '42',
  });
  assert.deepEqual(JSON.parse(JSON.stringify(saved)), {
    items: { a: { x: 6, y: 0, w: 6, h: 96 }, b: { x: 0, y: 0, w: 4 } },
    enabled: { a: true, b: false },
    updatedAt: 42,
  });
  assert.deepEqual(
    JSON.parse(JSON.stringify(dashboardLayout({ items: {}, enabled: { a: true } }))),
    { items: {}, enabled: { a: true } },
  );
  assert.equal(dashboardWidget({ id: 'a', key: '' }, 'tablet', saved).w, 6);
  assert.equal(dashboardWidget({ id: 'a', key: '' }, 'mobile', saved).w, 1);
});
test('source refresh truth and JavaScript timer conversion; no global pause control', () => {
  for (const raw of [undefined, null, '', false, 0])
    assert.equal(dashboardRefresh({ attrs: { refresh: raw } }, true, true), undefined);
  for (const [raw, delay] of [
    ['5', 5000],
    ['0', 0],
    [-1, -1000],
    [0.5, 500],
    [86401, 86401000],
  ])
    assert.equal(dashboardRefresh({ attrs: { refresh: raw } }, true, true), delay);
  assert.equal(dashboardRefresh({ attrs: { refresh: 5 } }, false, true), undefined);
  assert.equal(dashboardRefresh({ attrs: { refresh: 5 } }, true, false), undefined);
});

test('a successful refresh replaces rows, attributes and width instead of replaying the previous widget', () => {
  const old = {
    id: 'SystemProcess',
    key: 'default',
    name: '系统进程',
    attrs: { refresh: '1' },
    cols: { md: 8 },
    elements: [{ component: 'td', text: 'old' }],
  };
  const got = dashboardResponse(old, {
    attrs: { refresh: '2' },
    cols: { md: '6' },
    elements: [{ component: 'td', text: 'new' }],
  });
  assert.equal(got.id, old.id);
  assert.equal(got.attrs.refresh, '2');
  assert.equal(got.cols.md, '6');
  assert.equal(got.elements[0].text, 'new');
});

test('source local/server timestamp choice, absent row backfill and legacy enabled flags', () => {
  const newer = { items: {}, enabled: { SystemProcess: true }, updatedAt: 20 },
    older = { items: {}, updatedAt: 10 };
  assert.equal(dashboardPreferredLayout(newer, older).layout, newer);
  assert.equal(dashboardPreferredLayout(newer, undefined).write, true);
  assert.deepEqual(dashboardPreferredLayout({ ...newer, updatedAt: 5 }, older), {
    layout: { ...older, enabled: newer.enabled },
    write: true,
  });
  assert.deepEqual(dashboardPreferredLayout(undefined, { ...older, enabled: {} }), {
    layout: { ...older, enabled: {} },
    write: false,
  });
  assert.deepEqual(dashboardEnabled({ SystemProcess: 'false', disabled: 0 }), {
    SystemProcess: true,
    disabled: false,
  });
});

test('entering edit mode retains measured automatic height; explicit resize wins and reset restores measured height', () => {
  const item = { id: 'SystemProcess', key: '', name: '系统进程', cols: { md: 6 } };
  assert.equal(dashboardWidget(item, 'desktop', { items: {} }, 13).h, 13);
  assert.equal(
    dashboardWidget(item, 'desktop', { items: { SystemProcess: { x: 3, y: 0, w: 8, h: 20 } } }, 13)
      .h,
    20,
  );
  assert.equal(dashboardWidget(item, 'desktop', { items: {} }, 13).h, 13);
});
