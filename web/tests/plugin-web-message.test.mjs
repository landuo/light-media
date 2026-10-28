import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as message from '../src/features/plugin-web-message.ts';
const oracle = JSON.parse(
  readFileSync(
    new URL('../../testdata/standard-web-message/frontend-source-oracle.json', import.meta.url),
  ),
);
function fixture(name, api = async () => []) {
  const raw = readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'),
    { descriptor } = parse(raw),
    script = compileScript(descriptor, { id: name });
  const code = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const events = [],
    timers = [],
    cleanups = [],
    mod = { exports: {} };
  new Function('require', 'exports', 'window', 'HTMLElement', 'requestAnimationFrame', code)(
    (path) => {
      if (path === 'vue') return { ...vue, onMounted() {}, onUnmounted: (fn) => cleanups.push(fn) };
      if (path === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
      if (path === '../api') return { api, errorText: (e) => e.message };
      if (path === '../features/plugin-web-message') return message;
      if (path === '../features/system-messages')
        return { systemMessageRelativeTime: (value) => value };
      if (path.startsWith('vuetify/') || path.endsWith('.vue')) return {};
      throw Error(path);
    },
    mod.exports,
    {
      setTimeout(fn, ms) {
        timers.push({ fn, ms });
        return timers.length;
      },
      clearTimeout() {},
      getComputedStyle: () => ({ overflowY: 'auto' }),
    },
    Object,
    (fn) => {
      fn();
      return 1;
    },
  );
  const scope = vue.effectScope();
  const state = scope.run(() =>
    mod.exports.default.setup(
      { modelValue: true },
      { expose() {}, emit: (...v) => events.push(v) },
    ),
  );
  return {
    state,
    events,
    descriptor,
    timers,
    async flush() {
      let count = 0;
      while (timers.length && count++ < 30) {
        timers.shift().fn();
        await vue.nextTick();
      }
    },
    stop() {
      cleanups.forEach((fn) => fn());
      scope.stop();
    },
  };
}
for (const sample of oracle.sends)
  test('fixed Web send: ' + sample.input.name, async (t) => {
    const calls = [];
    let resolve;
    const pending = new Promise((r) => (resolve = r));
    const f = fixture('ShortcutMessageDialog', async (path, body, method) => {
      assert.equal(body, undefined, 'original post has no JSON/body echo');
      calls.push({ path: path.slice(1), method });
      if (sample.input.changed !== undefined) await pending;
      if (sample.input.fail) throw Error('owned transport failure');
      return sample.input.body;
    });
    t.after(f.stop);
    f.state.list.value = {
      $el: {
        scrollTop: 0,
        scrollHeight: 500,
        clientHeight: 200,
        addEventListener() {},
        removeEventListener() {},
      },
    };
    f.state.user_message.value = sample.input.text;
    const running = f.state.sendMessage();
    assert.deepEqual(
      { draft: f.state.user_message.value, disabled: f.state.sendButtonDisabled.value },
      sample.expected.during,
    );
    if (sample.input.changed !== undefined) {
      f.state.user_message.value = sample.input.changed;
      resolve();
    }
    await running;
    await f.flush();
    await vue.nextTick();
    const expectedCalls = sample.expected.calls.filter((x) => x !== 'scroll');
    assert.deepEqual(calls, expectedCalls);
    assert.equal(f.state.user_message.value, sample.expected.draft);
    assert.equal(f.state.sendButtonDisabled.value, sample.expected.disabled);
    assert.equal(
      f.state.list.value.$el.scrollTop,
      sample.expected.calls.includes('scroll') ? 300 : 0,
    );
    if (sample.input.fail) assert.equal(f.state.error.value, 'owned transport failure');
    assert.deepEqual(f.state.messages.value, [], 'sending never invents a chat echo or delivery');
  });
for (const sample of oracle.histories)
  test('fixed Web history: ' + sample.input.name, (t) => {
    const f = fixture('ShortcutMessageDialog');
    t.after(f.stop);
    for (let i = 0; i < sample.input.batches.length; i++) {
      const changed = f.state.mergeMessages(sample.input.batches[i]);
      assert.deepEqual(
        {
          changed,
          items: JSON.parse(JSON.stringify(f.state.messages.value)),
          lastTime: f.state.lastTime.value,
        },
        sample.expected[i],
      );
    }
  });
test('fixed message shortcut URL keeps raw query semantics and menu closes before dialog opens', (t) => {
  for (const c of oracle.shortcuts)
    assert.equal(message.pluginMessageShortcut(c.url), c.open, c.url);
  const f = fixture('ShortcutTools');
  t.after(f.stop);
  f.state.menu.value = true;
  f.state.openMessageDialog();
  assert.equal(f.state.menu.value, false);
  assert.equal(f.state.messages.value, true);
  assert.deepEqual(
    compileTemplate({
      source: f.descriptor.template.content,
      filename: 'ShortcutTools.vue',
      id: 'shortcut',
    }).errors,
    [],
  );
});
test('message history uses source page/size and advances only after nonempty success; failure retries same page', async (t) => {
  let response = [{ title: 'actual', reg_time: '2026-10-26 09:00:00', action: 1 }],
    failure = false;
  const paths = [],
    done = [];
  const f = fixture('ShortcutMessageDialog', async (path) => {
    paths.push(path);
    if (failure) throw Error('owned DB read failed');
    return response;
  });
  t.after(f.stop);
  await f.state.loadMessages({ done: (x) => done.push(x) });
  assert.equal(f.state.page.value, 2);
  assert.equal(f.state.messages.value.length, 1);
  failure = true;
  await f.state.loadMessages({ done: (x) => done.push(x) });
  assert.equal(f.state.page.value, 2);
  assert.equal(f.state.messages.value.length, 1);
  failure = false;
  response = [];
  await f.state.loadMessages({ done: (x) => done.push(x) });
  assert.equal(f.state.page.value, 2);
  assert.deepEqual(paths, [
    '/message/web?page=1&size=20',
    '/message/web?page=2&size=20',
    '/message/web?page=2&size=20',
  ]);
  assert.deepEqual(done, ['ok', 'error', 'empty']);
  assert.deepEqual(
    compileTemplate({
      source: f.descriptor.template.content,
      filename: 'ShortcutMessageDialog.vue',
      id: 'message',
    }).errors,
    [],
  );
  const script = f.descriptor.scriptSetup.content;
  assert.ok(
    !script.includes('EventSource'),
    'source shared FIFO is not consumed by a second dialog stream',
  );
  assert.ok(
    !script.includes("'/notifications'"),
    'ordinary history uses its own source-shaped endpoint',
  );
});

test('ordinary text Markdown keeps fixed MessageCard breaks/options/link attributes', () => {
  for (const c of oracle.markdown)
    assert.equal(message.renderPluginWebMessage(c.input), c.expected);
});

test('ordinary MessageCard image/link/note branches remain present and notes match fixed decoder', (t) => {
  for (const sample of oracle.notes)
    assert.deepEqual(message.pluginMessageNoteItems(sample.note), sample.expected);
  const f = fixture('ShortcutMessageDialog');
  t.after(f.stop);
  const template = f.descriptor.template.content;
  for (const marker of [
    'v-if="item.image"',
    ':src="item.image"',
    'openMessageLink(item.link)',
    'pluginMessageNoteItems(item.note)',
    'value.title_year',
    'value.enclosure',
    'value.vote_average',
    'value.description',
  ])
    assert.ok(template.includes(marker), marker);
});
