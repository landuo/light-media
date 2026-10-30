import './helpers/plugin-axios-fetch-adapter.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as forms from '../src/features/plugin-forms.ts';
import * as styles from '../src/features/plugin-page-styles.ts';
import * as dashboard from '../src/features/plugin-dashboard.ts';
import * as remoteAPI from '../src/features/plugin-remote-api.ts';
import { pluginJSONComponents } from '../src/features/plugin-json-components.ts';
import * as quickAccess from '../src/features/plugin-quick-access.ts';

function component(name, props, dependencies = {}) {
  const { descriptor } = parse(
    readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'),
  );
  const source = compileScript(descriptor, { id: name }).content.replaceAll(
    'import.meta.env.BASE_URL',
    "'/'",
  );
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const mod = { exports: {} },
    events = [],
    cleanups = [], activated = [], deactivated = [];
  const stubs = new Proxy({}, { get: (_, name) => ({ name }) });
  new Function('require', 'exports', 'window', 'document', 'navigator', 'localStorage', compiled)(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          useId: () => 'owned-ui',
          onMounted: () => {},
          onUnmounted: (fn) => cleanups.push(fn),
          onActivated: (fn) => activated.push(fn),
          onDeactivated: (fn) => deactivated.push(fn),
        };
      if (name === 'vuetify')
        return { useDisplay: () => ({ mdAndUp: vue.ref(true), mdAndDown: vue.ref(true) }) };
      if (name === 'vuetify/components')
        return Object.fromEntries(pluginJSONComponents.map((name) => [name, { name }]));
      if (name === '../features/plugin-forms') return forms;
      if (name === '../features/plugin-ace') return {};
      if (name === '../features/plugin-page-styles') return styles;
      if (name === '../features/plugin-dashboard') return dashboard;
      if (name === '../features/plugin-remote-api') return remoteAPI;
      if (name === '../features/plugin-quick-access') return quickAccess;
      if (name === '../features/interface-mode') return { appInterface: vue.ref(true) };
      if (name === '../features/plugin-icon-color') return {pluginIconColor:async()=> '#28A9E1'};
      if (name === '../features/plugin-clone') return { safePluginIcon: (value) => value };
      if (name === '../api')
        return {
          api: dependencies.api || (async () => null),
          errorText: (e) => e.message,
          toast: dependencies.toast || (() => {}),
        };
      if (name === '../features/plugin-federation') return dependencies.federation || {};
      if (name === '../features/plugin-navigation') return dependencies.navigation || {};
      if (
        name.startsWith('vuetify/') ||
        name.endsWith('.vue') ||
        name.endsWith('.css') ||
        name === 'gridstack' ||
        name === 'vue3-ace-editor' ||
        name.startsWith('ace-builds/')
      )
        return stubs;
      throw new Error('Unexpected import ' + name);
    },
    mod.exports,
    {
      innerWidth: 1200,
      scrollY: 0,
      matchMedia: () => ({ matches: true }),
      removeEventListener() {},
      requestAnimationFrame:()=>1, cancelAnimationFrame(){}, dispatchEvent(){},
      ...dependencies.window,
    },
    globalThis.document,
    globalThis.navigator,
    globalThis.localStorage,
  );
  const scope = vue.effectScope();
  const state = scope.run(() =>
    mod.exports.default.setup(props, { expose() {}, emit: (...values) => events.push(values) }),
  );
  return {
    state,
    events, activated, deactivated,
    stop() {
      cleanups.forEach((fn) => fn());
      scope.stop();
    },
  };
}

test('actual recursive form renders dynamic show/props, named model, slots, HTML and mutating handlers', async (t) => {
  const model = vue.reactive({ enabled: true, count: 2, tab: 'old', note: 'preserve' });
  const node = {
    component: 'VCard',
    props: { show: '{{ enabled && count > 1 }}', title: '{{ "Count " + count }}' },
    slots: { append: { component: 'VChip', text: 'slot' } },
    content: [
      { component: 'VBtn', props: { onClick: '(e) => { model.count += e }' }, text: 'Add' },
      { component: 'VTextField', props: { 'v-model:focused': 'tab' } },
      { component: 'div', html: '<b>trusted plugin markup</b>' },
    ],
  };
  assert.deepEqual(forms.pluginFormIssues([node]), []);
  const f = component('PluginFormRenderer', { node, model });
  t.after(f.stop);
  let rendered;
  const watcher = vue.effect(() => {
    rendered = f.state.render(node);
  });
  t.after(() => vue.stop(watcher));
  assert.equal(rendered.props.title, 'Count 2');
  assert.equal(rendered.props.style.display, '');
  assert.equal(rendered.children.append().children.default(), 'slot'); // Fixed FormRender single-object slot returns one VNode.
  const children = rendered.children.default();
  children[0].props.onClick(3);
  await Promise.resolve();
  assert.equal(model.count, 5);
  assert.equal(rendered.props.title, 'Count 5');
  children[1].props['onUpdate:focused']('new');
  assert.equal(model.tab, 'new');
  assert.equal(model.note, 'preserve');
  assert.equal(children[2].children.default().props.innerHTML, '<b>trusted plugin markup</b>'); // Source form HTML keeps the inner component layer.
  model.enabled = false;
  assert.equal(rendered.props.style.display, 'none');
  const failing = {
    component: 'VBtn',
    props: { onClick: '() => { throw new Error("owned handler failed") }' },
  };
  f.state.render(failing).props.onClick();
  await Promise.resolve();
  assert.equal(f.state.renderError.value, 'owned handler failed');
  assert.deepEqual(f.events.at(-1), ['error', 'owned handler failed']);
});

test('fixed JSON page refreshes after every fulfilled HTTP result and preserves data on503', async (t) => {
  const oldFetch = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = oldFetch;
  });
  const calls = [],
    reads = [];
  let response = { success: true }, status=200;
  globalThis.fetch = async (path, options) => {
    calls.push({ path, options });
    return new Response(JSON.stringify(response),{status});
  };
  const plugin = { instance_id: 'Demo_clone', plugin_id: 'Demo', version: '1.0', name: 'Demo' };
  const f = component(
    'PluginDataDialog',
    { plugin, showSwitch: false },
    {
      api: async (path) => {
        reads.push(path);
        return { render_mode: 'vuetify', page: [{ component: 'VCard', text: 'retained data' }] };
      },
    },
  );
  t.after(f.stop);
  await f.state.load();
  await f.state.pageAction({
    api: 'plugin/Demo/history',
    method: 'GET',
    params: { query: 'a & b' },
  });
  assert.equal(calls[0].path, '/api/v1/plugin/Demo/history?query=a+%26+b');
  assert.equal(reads.length, 2);
  await f.state.pageAction({
    api: 'plugin/Demo/clear',
    method: 'DELETE',
    params: { scope: 'owned' },
  });
  assert.equal(calls[1].options.method, 'POST', 'original JSON PageRender treats non-GET as POST');
  assert.equal(calls[1].options.body, '{"scope":"owned"}');
  response = { success: false, message: 'owned refused' };
  await f.state.pageAction({ api: 'plugin/Demo/clear', method: 'POST' });
  assert.equal(reads.length, 4,'fixed PageRender emits action after fulfilled200 even for business false');
  assert.equal(f.state.actionError.value,'');
  assert.equal(f.state.nodes.value[0].text, 'retained data');
  status=503;
  await f.state.pageAction({api:'plugin/Demo/clear',method:'POST'});
  assert.equal(reads.length,4,'HTTP503 must not emit a fulfilled action');
  assert.match(f.state.actionError.value,/503/);
  await f.state.pageAction({ api: 'plugin/Other/clear', method: 'POST' });
  assert.equal(calls.at(-1).path,'/api/v1/plugin/Other/clear','source Axios permits explicit other-plugin APIs');
  assert.equal(reads.length,4);
});

test('actual dashboard keeps previous widget and does not create one when get_dashboard returns None', async (t) => {
  const f = component('PluginDashboard', {}, { api: async () => null });
  t.after(f.stop);
  const meta = { id: 'Demo', key: 'summary', name: 'Demo' };
  await f.state.loadItem(meta);
  assert.deepEqual(f.state.items.value.map((item) => item.id), ['scheduler']);
  assert.equal(f.state.visible.value.length, 0, 'built-in scheduler defaults off');
  const existing = {
    ...meta,
    cols: { md: 6 },
    elements: [{ component: 'span', text: 'previous' }],
  };
  f.state.items.value = [existing];
  await f.state.loadItem(meta);
  assert.deepEqual(JSON.parse(JSON.stringify(f.state.items.value)), [existing]);
  assert.equal(f.state.timers.size, 0);
});

test('built-in scheduler shares ordinary settings and never calls a fake plugin getter', async (t) => {
  const calls = [];
  const f = component('PluginDashboard', {}, { api: async (path) => { calls.push(path); return []; } });
  t.after(f.stop);
  const builtin = f.state.items.value[0];
  assert.deepEqual(JSON.parse(JSON.stringify(builtin)), { id: 'scheduler', key: '', name: '后台任务', attrs: {}, cols: { cols: 12, md: 4 }, rows: 11, elements: [] });
  f.state.openSettings();
  assert.equal(f.state.settingItems.value[0].id, 'scheduler');
  f.state.layout.value.enabled = { scheduler: true };
  assert.equal(f.state.visible.value[0].id, 'scheduler');
  await f.state.loadItem(builtin);
  assert.deepEqual(calls, []);
});

test('fixed dashboard saves local snapshots before remote and closes/refetches after persistence failure', async (t) => {
  const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-navigation/frontend-oracle.json',import.meta.url)));
  const previous=Object.getOwnPropertyDescriptor(globalThis,'localStorage');
  t.after(()=>previous?Object.defineProperty(globalThis,'localStorage',previous):delete globalThis.localStorage);
  for(const fixture of oracle.saves) {
    const calls=[],saved=new Map();
    globalThis.localStorage={getItem:k=>saved.get(k),setItem:(k,v)=>{saved.set(k,v);calls.push('local:'+k.split('_').at(-1))}};
    const f=component('PluginDashboard',{}, {api:async(path,value)=>{
      if(path==='/plugins/dashboard/meta'){calls.push('meta');return []}
      const key=path.split('/').at(-1);calls.push('remote:'+key);
      return fixture.fail.replace('enable','enabled')===key?{saved:false,message:'owned persistence refused'}:{saved:true};
    }});t.after(f.stop);
    f.state.settingItems.value=[{id:'Owned',key:'one',name:'One'}];
    f.state.settingEnabled.value={'Owned:one':true};f.state.settings.value=true;
    await f.state.saveSettings();await Promise.resolve();
    assert.equal(f.state.settings.value,false,fixture.fail);
    assert.deepEqual(f.state.layout.value.enabled,fixture.expected.storage.enable);
    assert.deepEqual(JSON.parse(saved.get('LIGHT_MEDIA_PLUGIN_DASHBOARD_order')),fixture.expected.storage.order);
    assert.deepEqual(calls,fixture.expected.calls.filter(x=>x!=='close').map(x=>x.replace(/:enable$/,':enabled')));
    f.state.editing.value=true;await f.state.toggleEdit();
    assert.equal(f.state.editing.value,false,'source layout editor exits before async persistence');
  }
});

test('fixed dashboard shared settings use server first and preserve local fallback on read/backfill failure',async(t)=>{
  const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-navigation/frontend-oracle.json',import.meta.url)));
  const previous=Object.getOwnPropertyDescriptor(globalThis,'localStorage');
  t.after(()=>previous?Object.defineProperty(globalThis,'localStorage',previous):delete globalThis.localStorage);
  for(const fixture of oracle.cases){const saved=new Map(fixture.input.local?[['LIGHT_MEDIA_PLUGIN_DASHBOARD_test',JSON.stringify(fixture.input.local)]]:[]),calls=[];
    globalThis.localStorage={getItem:k=>saved.get(k),setItem:(k,v)=>saved.set(k,v)};
    const f=component('PluginDashboard',{}, {api:async(path,value)=>{if(value===undefined){calls.push({read:true,path});if(fixture.input.failRead)throw Error('owned read fail');return {value:fixture.input.remote}}
      calls.push({path,value});if(fixture.input.failWrite)throw Error('owned write fail');return {saved:true}}});t.after(f.stop);
    const got=await f.state.loadShared('test',value=>value&&typeof value==='object'?value:undefined);
    assert.deepEqual(got??null,fixture.expected.value,fixture.input.name);
    assert.deepEqual(calls.map(x=>({...x,path:x.path.replace('/plugins/dashboard/settings/test','/user/config/remote')})),fixture.expected.calls);
    assert.deepEqual(saved.has('LIGHT_MEDIA_PLUGIN_DASHBOARD_test')?JSON.parse(saved.get('LIGHT_MEDIA_PLUGIN_DASHBOARD_test')):null,fixture.expected.stored);
  }
});

test('dashboard activation refreshes only chosen metadata and deactivation stops timers',async(t)=>{
 const calls=[];const f=component('PluginDashboard',{}, {api:async path=>{calls.push(path);return null}});t.after(f.stop);
 f.state.metadata.value=[{id:'One',key:'',name:'One'},{id:'Two',key:'part',name:'Two'}];f.state.layout.value.enabled={One:true};
 f.activated[0]();await Promise.resolve();assert.deepEqual(calls,['/plugins/One/dashboard?key=']);
 f.deactivated[0]();f.state.schedule({id:'One',key:'',attrs:{refresh:1}});assert.equal(calls.length,1);assert.equal(f.state.timers.size,0);assert.equal(f.state.requesting.value,false);
});

test('actual PWA quick access opens only on eligible top pull, includes stopped plugins, and remembers last three', async (t) => {
  const saved = Object.fromEntries(
    ['document', 'navigator', 'localStorage'].map((key) => [
      key,
      Object.getOwnPropertyDescriptor(globalThis, key),
    ]),
  );
  t.after(() => {
    for (const [key, value] of Object.entries(saved)) {
      if (value) Object.defineProperty(globalThis, key, value);
      else delete globalThis[key];
    }
  });
  let online = true,
    overlay = null;
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      get onLine() {
        return online;
      },
    },
  });
  globalThis.document = {
    hidden: false,
    documentElement: { scrollTop: 0 },
    querySelector: () => overlay,
    removeEventListener() {},
  };
  const stored = new Map();
  globalThis.localStorage = {
    getItem: (key) => stored.get(key) ?? null,
    setItem: (key, value) => stored.set(key, value),
  };
  const cards = ['d', 'b', 'a', 'c'].map((instance_id) => ({
    instance_id,
    plugin_id: instance_id,
    name: instance_id,
    has_page: true,
    state: false,
    installed: true,
    config: 'must-not-persist',
  }));
  let reads = 0;
  const f = component(
    'PluginQuickAccess',
    {},
    {
      api: async (path) => {
        assert.equal(path,'/plugins?state=installed');
        reads++;
        return [...cards, { instance_id: 'no-page', has_page: false }];
      },
    },
  );
  t.after(f.stop);
  const pull = () => {
    f.state.touchStart({ touches: [{ clientY: 0 }] });
    f.state.touchMove({ touches: [{ clientY: 160 }], cancelable: true, preventDefault() {} });
    f.state.touchEnd();
  };
  online = false;
  pull();
  assert.equal(f.state.open.value, false);
  online = true;
  overlay = {};
  pull();
  assert.equal(f.state.open.value, false);
  overlay = null;
  document.documentElement.scrollTop = 101;
  pull();
  assert.equal(f.state.open.value, false);
  document.documentElement.scrollTop = 0;
  pull();
  await Promise.resolve();
  assert.equal(reads, 1);
  assert.equal(f.state.open.value, true);
  assert.deepEqual(
    f.state.items.value.map((item) => item.name),
    ['a', 'b', 'c', 'd'],
  );
  for (const card of cards) f.state.choose(card);
  f.state.choose(cards[1]);
  assert.deepEqual(
    f.state.recent.value.map((item) => item.instance_id),
    ['b', 'c', 'a'],
  );
  assert.equal(f.state.selected.value.instance_id, 'b');
  assert.equal(f.state.open.value, false,'opening details closes quick access');
  assert.equal(typeof f.state.recent.value[0].access_time,'number');
  f.state.selected.value=undefined;f.state.open.value=true;
  const touch={touches:[{clientY:200}],stopPropagation(){},preventDefault(){}};
  f.state.bottomStart(touch);f.state.bottomMove({...touch,touches:[{clientY:90}]});f.state.bottomEnd(touch);
  assert.equal(f.state.open.value,false,'source bottom swipe >=100px closes panel');
  assert.ok(!stored.get(quickAccess.recentPluginKey).includes('must-not-persist'));
});

test('fixed services submit without awaiting execution and refresh after one second', async (t) => {
  const sourceOracle=JSON.parse(readFileSync(new URL('../../testdata/standard-services/frontend-oracle.json',import.meta.url)));
  const oldDocument = globalThis.document, oldSetTimeout=globalThis.setTimeout, oldClearTimeout=globalThis.clearTimeout;
  const timers=[];
  globalThis.document = { hidden: false, removeEventListener() {} };
  globalThis.setTimeout=(fn,ms)=>{const timer={fn,ms};timers.push(timer);return timer};
  globalThis.clearTimeout=()=>{};
  t.after(() => {globalThis.setTimeout=oldSetTimeout;globalThis.clearTimeout=oldClearTimeout;
    if (oldDocument === undefined) delete globalThis.document; else globalThis.document = oldDocument;
  });
  const toasts=[],requests=[];
  let finish;
  const pending=new Promise(resolve=>{finish=resolve});
  const item={id:'Demo_tick',instance_id:'Demo',service_id:'tick',can_run:true,status:'等待',next_run:'1小时'};
  const f=component('PluginServices',{}, {toast:text=>toasts.push(text),api:async path=>{requests.push(path);return path.includes('runscheduler')?pending:[{...item}]}});
  t.after(f.stop);
  f.state.run(item);
  assert.deepEqual(requests,['/v1/'+sourceOracle.immediate.requests[0].path+'?jobid='+sourceOracle.immediate.requests[0].options.params.jobid]);
  assert.deepEqual(toasts,['定时作业执行请求提交成功！']);
  assert.equal(f.state.items.value.length,0,'pending execution must not gate the refresh timer');
  assert.equal(timers[0].ms,sourceOracle.immediate.timer_ms[0]);
  timers[0].fn(); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(requests,['/v1/system/runscheduler?jobid=Demo_tick','/plugins/services']);
  assert.equal(f.state.items.value[0].status,'等待');
  f.state.run({...item,status:'正在运行'});
  assert.equal(requests.length,2,'running rows are not submitted again');
  finish({success:true});await Promise.resolve();
  const source=readFileSync(new URL('../src/components/PluginServices.vue',import.meta.url),'utf8');
  assert.doesNotMatch(source,/VProgressLinear|progress_detail/,'fixed service page has no progress/failure replacement UI');
});

test('page and dashboard recursive nodes retain both their own text and nested children', (t) => {
  const node = {
    component: 'VCard',
    text: 'heading',
    content: [{ component: 'VBtn', text: 'child' }],
  };
  for (const view of ['page', 'dashboard']) {
    const f = component('PluginFormRenderer', { node, model: {}, view });
    t.after(f.stop);
    const contents = f.state.render(node).children.default();
    assert.equal(contents[0], 'heading');
    const nested = view === 'page'
      ? component('PluginFormRenderer', contents[1].props)
      : null;
    if (nested) t.after(nested.stop);
    const child = nested ? nested.state.render(contents[1].props.node) : contents[1];
    assert.equal(child.type.name, 'VBtn');
    assert.deepEqual(child.children.default(), ['child']);
  }
});

test('actual desktop and mobile dashboards share enabled choices while preserving independent grid layouts', async (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const local = new Map();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: (key) => local.get(key), setItem: (key, value) => local.set(key, value) },
  });
  t.after(() =>
    previous
      ? Object.defineProperty(globalThis, 'localStorage', previous)
      : delete globalThis.localStorage,
  );
  const store = {
    enabled: { 'Demo:summary': true },
    desktop: {
      items: { 'Demo:summary': { x: 4, y: 2, w: 6, h: 5 } },
      enabled: { 'Demo:summary': false },
    },
    mobile: {
      items: { 'Demo:summary': { x: 0, y: 7, w: 1, h: 9 } },
      enabled: { 'Demo:summary': true },
    },
  };
  const requests = [];
  const api = async (path, value) => {
    const key = path.split('/').at(-1);
    if (value === undefined) return { value: structuredClone(store[key]) };
    requests.push(key);
    store[key] = structuredClone(value);
    return { saved: true };
  };
  const desktop = component('PluginDashboard', {}, { api });
  t.after(desktop.stop);
  desktop.state.layout.value.enabled = await desktop.state.loadShared('enabled',dashboard.dashboardEnabled);
  desktop.state.layout.value = await desktop.state.loadLayout('desktop');
  assert.equal(
    desktop.state.layout.value.enabled['Demo:summary'],
    true,
    'shared preference wins over old desktop flags',
  );
  desktop.state.settingEnabled.value = { 'Demo:summary': false };
  desktop.state.settings.value = true;
  await desktop.state.saveSettings();
  assert.deepEqual(requests, ['enabled', 'order']);
  assert.equal(store.desktop.items['Demo:summary'].x, 4);
  assert.equal(store.mobile.items['Demo:summary'].y, 7);
  local.clear(); // A different browser reads the same shared server preference.
  const mobile = component('PluginDashboard', {}, { api, window: { innerWidth: 390 } });
  t.after(mobile.stop);
  mobile.state.layout.value.enabled = await mobile.state.loadShared('enabled',dashboard.dashboardEnabled);
  mobile.state.layout.value = await mobile.state.loadLayout('mobile');
  assert.equal(mobile.state.layout.value.enabled['Demo:summary'], false);
  assert.deepEqual(mobile.state.layout.value.items['Demo:summary'], { x: 0, y: 7, w: 1, h: 9 });
  await mobile.state.saveLayout();
  assert.equal(
    Object.hasOwn(store.mobile, 'enabled'),
    false,
    'grid saves cannot fork visibility by device',
  );
  assert.deepEqual(store.enabled, { 'Demo:summary': false });
  assert.deepEqual(store.desktop.items['Demo:summary'], { x: 4, y: 2, w: 6, h: 5 });
});

test('fixed AppPage loads URL directly without sidebar membership and watches identity/navigation',async(t)=>{
 const calls=[],pending=[];let unrelated=0;
 const props=vue.reactive({route:'plugin-app/Owned'});
 const f=component('PluginAppPage',props,{api:async()=>{unrelated++;throw Error('unexpected membership gate')},navigation:{parsePluginNavigationRoute(route){const [,instance,navKey='main']=route.split('/');return {instance,navKey}}},federation:{loadPluginAppRemote(instance,key,options){calls.push({instance,key,signal:options.signal});return new Promise(resolve=>pending.push(resolve))}}});t.after(f.stop);
 assert.deepEqual(calls.map(x=>[x.instance,x.key]),[['Owned','main']]);
 props.route='plugin-app/Ownedcopy/my-tool';await vue.nextTick();assert.equal(calls[0].signal.aborted,true);
 pending[0]({component:{render(){}},descriptor:{plugin_id:'Owned',instance_id:'Owned'}});await Promise.resolve();assert.equal(f.state.pluginID.value,'');
 pending[1]({component:{render(){}},descriptor:{plugin_id:'Owned',instance_id:'Ownedcopy'}});await Promise.resolve();assert.equal(f.state.pluginID.value,'Ownedcopy');assert.equal(f.state.navKey.value,'my-tool');assert.equal(unrelated,0);
});

test('fixed grid persists drag/resize immediately, keeps width-only height automatic and exits after reset',async(t)=>{
 const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-navigation/frontend-oracle.json',import.meta.url)));
 const previous=Object.getOwnPropertyDescriptor(globalThis,'localStorage');
 t.after(()=>previous?Object.defineProperty(globalThis,'localStorage',previous):delete globalThis.localStorage);
 for(const sample of oracle.layout.resizes){
  const writes=[],stored=new Map();globalThis.localStorage={getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)};
  const f=component('PluginDashboard',{}, {api:async(path,value)=>{writes.push({path,value});return {saved:true}}});t.after(f.stop);
  const node={id:'Owned:summary',w:6,x:0,y:0,h:sample.end};let compacts=0;
  f.state.grid={destroy(){},compact(){compacts++},getColumn(){return 12},save(){return [node]},setStatic(){},batchUpdate(){},engine:{nodes:[]}};
  f.state.container.value={querySelectorAll(){return []}};
  const element={getAttribute:()=>node.id,gridstackNode:{h:sample.begin}};
  f.state.resizeStarted(null,element);element.gridstackNode.h=sample.end;f.state.resizeStopped(null,element);
  await new Promise(setImmediate);
  assert.equal(compacts,1);assert.equal(writes.length,1);
  const expectedManual=sample.expected.find(x=>x.persist!==undefined).persist;
  assert.equal(Object.hasOwn(writes[0].value.items[node.id],'h'),Boolean(expectedManual),sample.name);
  f.state.dragStopped();await new Promise(setImmediate);assert.equal(writes.length,2,'drag stop persists while editor is still active');
  f.state.editing.value=true;f.state.grid.removeAll=()=>{};
  await f.state.resetLayout();await new Promise(setImmediate);
  assert.equal(f.state.editing.value,oracle.layout.reset.editing);assert.deepEqual(f.state.layout.value.items,oracle.layout.reset.items);
 }
});

test('fixed complete pull gesture preserves continued motion, scroll boundary, dialog cache and content following',async(t)=>{
 const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-navigation/gesture-source-oracle.json',import.meta.url)));
 const previous=Object.fromEntries(['document','navigator','localStorage'].map(k=>[k,Object.getOwnPropertyDescriptor(globalThis,k)])),oldNow=Date.now,oldTimeout=globalThis.setTimeout,oldClear=globalThis.clearTimeout;
 t.after(()=>{Date.now=oldNow;globalThis.setTimeout=oldTimeout;globalThis.clearTimeout=oldClear;for(const[k,v]of Object.entries(previous)){if(v)Object.defineProperty(globalThis,k,v);else delete globalThis[k]}});
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{onLine:true}});globalThis.localStorage={getItem:()=>null,setItem(){}};
 for(const c of oracle.cases){let now=1000,scroll=c.input.initialScroll||0,overlay=false,prevented=0;const timers=[];
  Date.now=()=>now;globalThis.setTimeout=(fn,ms)=>{timers.push(ms);return {}};globalThis.clearTimeout=()=>{};
  globalThis.document={hidden:false,documentElement:{scrollTop:0},querySelector:()=>overlay?{}:null,removeEventListener(){}};
  const f=component('PluginQuickAccess',{}, {api:async()=>[],window:{get scrollY(){return scroll}}});t.after(f.stop);
  f.state.touchStart({touches:[{clientY:0}]});for(const move of c.input.moves){now+=move.elapsed||1;if(move.scroll!==undefined)scroll=move.scroll;if(move.overlay!==undefined)overlay=move.overlay;f.state.touchMove({touches:[{clientY:move.y}],cancelable:true,preventDefault(){prevented++}})}
  assert.deepEqual({distance:f.state.distance.value,pulling:f.state.pulling.value,transform:f.state.pullStyle.value.transform,transition:f.state.pullStyle.value.transition,indicator:f.state.pulling.value&&f.state.distance.value>=60},c.expected.before,c.input.name);
  f.state.touchEnd();assert.equal(f.state.open.value,Boolean(c.expected.triggered),c.input.name);assert.equal(prevented,c.expected.prevented,c.input.name);assert.deepEqual(timers,c.expected.timer_ms,c.input.name);
  f.stop();
 }
});

test('dashboard node retains its remote component and API when periodic data refresh preserves identity', async t => {
 // Fixed DashboardElement.vue:103 caches this node's load promise and
 // waitForDashboardElementLoaded():164 returns after its first load. Replacing
 // config data for the same widget must not tear down an open child dialog.
 const props=vue.reactive({item:{id:'Owned',key:'summary',render_mode:'vue',attrs:{refresh:2}},allowRefresh:true});
 let loads=0;const remote={name:'OwnedStableDashboard'};
 const f=component('PluginDashboardWidget',props,{federation:{loadPluginRemote:async(id,name,{signal})=>{
  loads++;return {component:remote,descriptor:{plugin_id:'Owned',instance_id:id}};
 }}});t.after(f.stop);
 await Promise.resolve();await vue.nextTick();
 assert.equal(loads,1);const first=f.state.remote.value,api=f.state.remoteAPI.value;
 props.item={...props.item,attrs:{refresh:2,title:'updated data'}};
 await vue.nextTick();await Promise.resolve();await vue.nextTick();
 assert.equal(loads,1,'periodic metadata reload remounted the remote child');
 assert.equal(f.state.remote.value,first);assert.equal(f.state.remoteAPI.value,api);
 props.item={...props.item,key:'details'};await vue.nextTick();await Promise.resolve();await vue.nextTick();
 assert.equal(loads,2,'a different widget identity still loads its component');
});


test('fixed PageRender retains a removed listener until its recursive node is unmounted', async t => {
 const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-render/source-oracle.json',import.meta.url)));
 const calls=[],props=vue.reactive({node:{component:'VBtn',events:oracle.page_event_cases.steps[0].events,text:'Owned'},model:{},view:'page',action:async event=>calls.push([event.api,event.method,event.params])});
 const f=component('PluginFormRenderer',props);t.after(f.stop);
 for(let i=0;i<oracle.page_event_cases.steps.length;i++){
  props.node={component:'VBtn',text:'Owned',...oracle.page_event_cases.steps[i]};await vue.nextTick();
  const rendered=f.state.render(props.node);
  assert.equal(typeof rendered.props.onClick,'function','source listener disappeared before node unmount');
  await rendered.props.onClick();assert.deepEqual(calls,oracle.page_event_cases.calls[i]);
 }
 const next=component('PluginFormRenderer',{...props,node:{component:'VBtn',text:'New node',events:{}}});t.after(next.stop);
 assert.equal(next.state.render(next.state.props?.node||{component:'VBtn',events:{},text:'New node'}).props?.onClick,undefined,'a fresh node must not inherit removed node listeners');
});
