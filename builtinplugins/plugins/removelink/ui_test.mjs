import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {test} from 'node:test';

// Uses the repository's already installed real Vue runtime. No new package,
// fake reactivity, browser, network call or product Python runtime is involved.
const require = createRequire(process.env.REMOVELINK_VUE_PACKAGE || import.meta.url);
const Vue = require('vue');
const load = async path => import(`data:text/javascript;base64,${Buffer.from(await readFile(new URL(path, import.meta.url))).toString('base64')}`);
const source = await load('./reference/source-view.mjs');
const target = await load('./frontend/view.js');

function mount(module,initialConfig) {
  const renderer = Vue.createRenderer({
    createElement: type => ({type,props:{},children:[],parent:null,text:''}),
    createText: text => ({type:'#text',props:{},children:[],parent:null,text}),
    createComment: text => ({type:'#comment',props:{},children:[],parent:null,text}),
    setText(node,text){node.text=text;},setElementText(node,text){node.text=text;node.children=[];},
    patchProp(node,key,old,value){node.props[key]=value;},
    insert(node,parent,anchor){if(node.parent){const at=node.parent.children.indexOf(node);if(at>=0)node.parent.children.splice(at,1);}node.parent=parent;const at=anchor?parent.children.indexOf(anchor):-1;if(at<0)parent.children.push(node);else parent.children.splice(at,0,node);},
    remove(node){if(node.parent){const at=node.parent.children.indexOf(node);if(at>=0)node.parent.children.splice(at,1);}node.parent=null;},
    parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  });
  const events=[];const root={type:'root',children:[],props:{}};
  const app=renderer.createApp(module.createConfig(Vue),{initialConfig,onSave:value=>events.push(['save',value]),onClose:()=>events.push(['close']),onLayout:value=>events.push(['layout',value])});app.mount(root);
  const all=()=>{const out=[];const walk=n=>{out.push(n);for(const c of n.children||[])walk(c);};walk(root);return out;};
  const text=n=>String(n.text||'')+(n.children||[]).map(text).join('');
  const find=fn=>{const node=all().find(fn);assert.ok(node,'control exists');return node;};
  const click=async label=>{find(n=>n.type==='button'&&text(n)===label).props.onClick();await Vue.nextTick();};
  const edit=async(label,value)=>{find(n=>n.props['aria-label']===label).props.onInput({target:{value}});await Vue.nextTick();};
  return {events,all,text,find,click,edit,unmount:()=>app.unmount()};
}

for(const [name,module] of [['source',source],['target',target]]) {
  test(`${name}: exact13 defaults and finite numeric validation`,()=>{
    assert.equal(Object.keys(module.DEFAULTS).length,13);
    for(const value of [10,86400,'30',30.0])assert.equal(module.configPayload({...module.DEFAULTS,delay_seconds:value}).delay_seconds,Number(value));
    for(const value of [null,false,9,86401,1.5,'bad',{},[]])assert.throws(()=>module.configPayload({...module.DEFAULTS,delayed_deletion:false,delay_seconds:value}));
  });
  test(`${name}: actual Vue drafts across four tabs, unknown config, save/cancel`,async()=>{
    const original={enabled:false,unknown:{nested:[1,'汉字']},delay_seconds:'30'};const before=JSON.stringify(original);const view=mount(module,original);
    assert.deepEqual(view.events,[['layout',{maxWidth:'1120px'}]]);
    await view.edit('目录列表','/owned/source\n/owned/media');
    await view.click('STRM 联动');await view.edit('STRM 路径映射','/owned/strm:local:/owned/media');
    await view.click('清理选项');assert.equal(view.find(n=>n.props['aria-label']==='自定义后缀').props.disabled,true);
    await view.click('运行设置');view.find(n=>n.props['aria-label']==='启用插件').props.onChange({target:{checked:true}});await Vue.nextTick();
    await view.click('硬链接');assert.equal(view.find(n=>n.props['aria-label']==='目录列表').props.value,'/owned/source\n/owned/media');
    await view.click('保存设置');const payload=view.events.at(-1)[1];assert.equal(payload.enabled,true);assert.equal(payload.delay_seconds,30);assert.deepEqual(payload.unknown,original.unknown);assert.equal(payload.strm_path_mappings,'/owned/strm:local:/owned/media');
    assert.equal(JSON.stringify(original),before);await view.click('取消');assert.deepEqual(view.events.at(-1),['close']);view.unmount();
  });
  test(`${name}: disabled delay is still validated and error selects links`,async()=>{
    const view=mount(module,{delay_seconds:9,delayed_deletion:false});await view.click('运行设置');await view.click('保存设置');
    assert.equal(view.events.filter(e=>e[0]==='save').length,0);assert.equal(view.find(n=>n.props.role==='region').props['aria-label'],'硬链接');assert.match(view.text(view.find(n=>n.props.role==='alert')),/10 至 86400/);view.unmount();
  });
}
test('target: user-scoped header/storage exclusions preserve actions and form semantics',async()=>{
  const view=mount(target,{});assert.ok(!view.all().some(n=>n.type==='h2'));assert.ok(!view.text(view.find(n=>n.type==='form')).includes('LIBRARY · CARE'));await view.click('STRM 联动');const text=view.text(view.find(n=>n.type==='form'));assert.ok(!text.includes('115 视频'));assert.ok(!text.includes('alipan'));assert.ok(text.includes('/ssd/strm:local:/media'));view.unmount();
});
test('source and target payload behavior is equal for unknown config and numeric spellings',()=>{
  for(const delay of [10,'10',30,86400]){const cfg={...source.DEFAULTS,delay_seconds:delay,unknown:{v:['x',1]},strm_path_mappings:'/one:local:/two'};assert.deepEqual(target.configPayload(cfg),source.configPayload(cfg));}
});
