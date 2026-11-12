// Exercise the actual Vuetify windowing engine used by FileManager; synthetic
// viewport geometry is not a browser screenshot or a rendering benchmark.
import test from 'node:test';
import assert from 'node:assert/strict';
import * as vue from 'vue';
import { useVirtual } from 'vuetify/lib/composables/virtual.mjs';
import { DisplaySymbol } from 'vuetify/lib/composables/display.mjs';

const renderer = vue.createRenderer({
  createComment: () => ({}),
  insert() {},
  remove() {},
  parentNode() {},
  nextSibling() {},
});
test('10,000 variable-height files keep a bounded window through scrolling and resize', async (t) => {
  const previous = Object.fromEntries(
    ['document', 'requestAnimationFrame', 'cancelAnimationFrame'].map((key) => [
      key,
      globalThis[key],
    ]),
  );
  const frames = new Map();
  let frame = 0;
  globalThis.document = { documentElement: {} };
  globalThis.requestAnimationFrame = (fn) => {
    frames.set(++frame, fn);
    return frame;
  };
  globalThis.cancelAnimationFrame = (id) => frames.delete(id);
  const flush = async () => {
    await vue.nextTick();
    for (const [id, fn] of [...frames]) {
      frames.delete(id);
      fn();
    }
    await vue.nextTick();
  };
  let virtual;
  const items = vue.ref(Array.from({ length: 10000 }, (_, i) => ({ path: `/file-${i}` })));
  const props = vue.reactive({ itemHeight: 62, height: 620 });
  const app = renderer.createApp({
    name: 'FileWindowTest',
    setup() {
      virtual = useVirtual(props, items);
      virtual.containerRef.value = { scrollTop: 0 };
      virtual.markerRef.value = { offsetTop: 0 };
      return () => null;
    },
  });
  app.provide(DisplaySymbol, { height: vue.ref(620) });
  app.mount({});
  t.after(() => {
    app.unmount();
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete globalThis[key];
      else globalThis[key] = value;
    }
  });
  await flush();
  assert.ok(virtual.computedItems.value.length <= 15);
  assert.equal(virtual.computedItems.value[0].index, 0);
  virtual.handleItemResize(0, 186);
  await new Promise((resolve) => setTimeout(resolve, 20));
  virtual.scrollToIndex(5000);
  virtual.handleScroll();
  await flush();
  assert.ok(virtual.computedItems.value.some((item) => item.index === 5000));
  assert.ok(virtual.computedItems.value.length <= 15);
  virtual.scrollToIndex(9999);
  virtual.handleScroll();
  await flush();
  assert.equal(virtual.computedItems.value.at(-1).index, 9999);
  assert.ok(virtual.computedItems.value.length <= 15);
  props.height = 1240;
  await flush();
  virtual.scrollToIndex(100);
  virtual.handleScroll();
  await flush();
  assert.ok(virtual.computedItems.value.length <= 25);
  assert.ok(virtual.computedItems.value.some((item) => item.index === 100));
});
