import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

export const serviceRow = (id) => ({
  id,
  instance_id: 'Owned',
  service_id: id,
  provider: 'Owned provider',
  name: 'Owned ' + id,
  status: '等待',
  next_run: null,
  can_run: true,
});

// Execute the actual SFC script. When sourceRoot is supplied, execute the entire
// fixed useBackground and BackgroundManager modules too, never a timer model.
// Only API, DOM events, clocks, translation, toast and unused SSE are boundaries.
export function servicesFixture({
  sourceRoot,
  targetPath,
  initialHidden = false,
  initialMode = 'ok',
} = {}) {
  const source = Boolean(sourceRoot);
  const files = {},
    starts = [],
    stops = [],
    timers = new Map(),
    listeners = new Map();
  const requests = [],
    toasts = [],
    logs = [],
    heldReads = [],
    submissions = [];
  let serial = 0,
    now = 0,
    mode = initialMode,
    rowID = 'initial';
  const read = (path) => {
    const text = readFileSync(path, 'utf8');
    files[String(path)] = createHash('sha256').update(text).digest('hex');
    return text;
  };
  const eventTarget = (prefix) => ({
    addEventListener(type, fn) {
      const key = prefix + type;
      if (!listeners.has(key)) listeners.set(key, new Set());
      listeners.get(key).add(fn);
    },
    removeEventListener(type, fn) {
      const key = prefix + type;
      listeners.get(key)?.delete(fn);
      if (!listeners.get(key)?.size) listeners.delete(key);
    },
  });
  const document = { hidden: initialHidden, ...eventTarget('document:') };
  const window = eventTarget('window:');
  const addTimer = (fn, ms, repeating) => {
    const id = ++serial;
    timers.set(id, { fn, ms, repeating, due: now + ms });
    return id;
  };
  const clearTimer = (id) => timers.delete(id);
  const request = (path, options) => {
    const isRun = path.includes('runscheduler');
    const url = new URL(path, 'https://owned.invalid/');
    requests.push({
      at: now,
      path,
      ...(options ? { options } : {}),
      kind: isRun ? 'run' : 'read',
      ...(isRun ? { jobid: options?.params?.jobid ?? url.searchParams.get('jobid') } : {}),
    });
    if (isRun) {
      const promise = new Promise((resolve, reject) => submissions.push({ resolve, reject }));
      // Observe API-boundary rejection so the original fire-and-forget promise
      // does not become a process-level unhandled rejection in this test host.
      promise.catch(() => {});
      return promise;
    }
    if (mode === 'failure') return Promise.reject(new Error('Owned injected read failure'));
    if (mode === 'held')
      return new Promise((resolve, reject) => heldReads.push({ resolve, reject }));
    return Promise.resolve(mode === 'empty' ? [] : [serviceRow(rowID)]);
  };
  const translations = {
    'setting.scheduler.executeSuccess': '定时作业执行请求提交成功！',
    'setting.scheduler.running': '正在运行',
    'setting.scheduler.stopped': '停止',
    'setting.scheduler.waiting': '等待',
    'setting.scheduler.noService': '没有后台服务',
  };
  function evaluate(code, imports) {
    const exports = {};
    const js = ts.transpileModule(code, {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
      },
    }).outputText;
    new Function(
      'require',
      'exports',
      'document',
      'window',
      'setInterval',
      'clearInterval',
      'setTimeout',
      'clearTimeout',
      'console',
      'ref',
      'Date',
      js,
    )(
      (key) => {
        if (key === 'vue')
          return {
            ...vue,
            onMounted: (fn) => starts.push(fn),
            onUnmounted: (fn) => stops.push(fn),
          };
        if (key in imports) return imports[key];
        if (key.startsWith('vuetify/')) return {};
        throw Error('Unexpected import: ' + key);
      },
      exports,
      document,
      window,
      (fn, ms) => addTimer(fn, ms, true),
      clearTimer,
      (fn, ms) => addTimer(fn, ms, false),
      clearTimer,
      {
        log: (...args) => logs.push(args.map(String)),
        error: (...args) => logs.push(args.map(String)),
      },
      vue.ref,
      class extends Date {
        static now() {
          return now;
        }
      },
    );
    return exports;
  }
  let imports;
  if (source) {
    const manager = evaluate(read(sourceRoot + '/utils/backgroundManager.ts'), {});
    const background = evaluate(read(sourceRoot + '/composables/useBackground.ts'), {
      '@/utils/backgroundManager': manager,
      '@/utils/sseManager': new Proxy(
        {},
        {
          get() {
            throw Error('Unused SSE boundary was called');
          },
        },
      ),
    });
    imports = {
      '@/api': { get: request },
      '@/composables/useBackground': background,
      'vue-i18n': { useI18n: () => ({ t: (key) => translations[key] ?? key }) },
      'vue-toastification': {
        useToast: () => ({ success: (text) => toasts.push({ text, error: false, at: now }) }),
      },
    };
  } else {
    imports = {
      '../api': {
        api: request,
        errorText: (e) => e.message,
        toast: (text, error = false) => toasts.push({ text, error, at: now }),
      },
    };
  }
  const path = source
    ? sourceRoot + '/views/system/ServiceView.vue'
    : (targetPath ?? new URL('../../src/components/PluginServices.vue', import.meta.url));
  const { descriptor } = parse(read(path));
  const script = compileScript(descriptor, { id: 'owned-services-lifecycle' }).content;
  const component = evaluate(script, imports).default;
  const state = component.setup({}, { expose() {} });
  const settle = async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  };
  const mount = async () => {
    for (const fn of starts) void fn();
    await settle();
  };
  const stop = () => {
    for (const fn of stops) fn();
  };
  const hidden = async (value) => {
    document.hidden = value;
    for (const fn of listeners.get('document:visibilitychange') ?? []) fn();
    await settle();
  };
  const advance = async (ms) => {
    const end = now + ms;
    for (;;) {
      const next = [...timers]
        .filter(([, timer]) => timer.due <= end)
        .sort((a, b) => a[1].due - b[1].due || a[0] - b[0])[0];
      if (!next) break;
      const [id, timer] = next;
      now = timer.due;
      if (timer.repeating) timer.due += timer.ms;
      else timers.delete(id);
      timer.fn();
      await settle();
    }
    now = end;
    await settle();
  };
  const snapshot = (label) => ({
    label,
    at: now,
    requests: requests.map(({ at, kind, jobid }) => ({ at, kind, ...(jobid ? { jobid } : {}) })),
    rows: JSON.parse(JSON.stringify((source ? state.schedulerList : state.items).value)),
    toasts: structuredClone(toasts),
    timers: [...timers].map(([id, { ms, due, repeating }]) => ({ id, ms, due, repeating })),
    visibilityListeners: listeners.get('document:visibilitychange')?.size ?? 0,
  });
  return {
    source,
    files,
    state,
    descriptor,
    requests,
    toasts,
    logs,
    timers,
    listeners,
    heldReads,
    submissions,
    mount,
    stop,
    hidden,
    advance,
    settle,
    snapshot,
    mode(value, id = value) {
      mode = value;
      rowID = id;
    },
    run(row = serviceRow('Owned_tick')) {
      source ? state.runCommand(row.id) : state.run(row);
    },
  };
}
