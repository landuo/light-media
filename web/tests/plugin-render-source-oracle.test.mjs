import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parse,compileScript} from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as forms from '../src/features/plugin-forms.ts';
import {pluginJSONComponents} from '../src/features/plugin-json-components.ts';
import * as styles from '../src/features/plugin-page-styles.ts';
const reference=JSON.parse(readFileSync(new URL('../../testdata/standard-render/source-oracle.json',import.meta.url)));
const source=readFileSync(new URL('../src/components/PluginFormRenderer.vue',import.meta.url),'utf8').replaceAll('import.meta.env.BASE_URL',JSON.stringify('/'));
const {descriptor}=parse(source);
const compiled=ts.transpileModule(compileScript(descriptor,{id:'source-render-oracle'}).content,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const module={exports:{}};
new Function('require','exports',compiled)(name=>{
 if(name==='vue')return vue;
 if(name==='../features/plugin-forms')return forms;
 if(name==='../features/plugin-page-styles')return styles;
 if(name==='../features/plugin-ace')return {};
 if(name==='vuetify/components')return Object.fromEntries(pluginJSONComponents.map(name=>[name,{name}]));
 if(name.startsWith('vuetify/'))return new Proxy({},{get:(_,key)=>({name:String(key)})});
 if(name==='vue3-ace-editor')return {VAceEditor:{name:'VAceEditor'}};
 if(name.endsWith('.vue'))return {name:'PluginDialogCloseBtn'};
 if(name.startsWith('ace-builds/'))return {};
 throw Error('unexpected renderer import '+name);
},module.exports);
function normalize(x){
 if(x===undefined)return {$undefined:true};
 if(typeof x==='function')return '$function';
 if(Array.isArray(x))return x.map(normalize);
 if(vue.isVNode(x)){
  const out={component:typeof x.type==='string'?x.type:x.type.name,props:normalize(x.props||{})};
  if(x.children&&typeof x.children==='object'&&!Array.isArray(x.children))out.slots=Object.fromEntries(Object.entries(x.children).filter(([k])=>k!=='_ctx').map(([k,v])=>[k,normalize(typeof v==='function'?v({ownedScope:1}):v)]));
  else out.content=normalize(x.children);
  return out;
 }
 if(x&&typeof x==='object')return Object.fromEntries(Object.entries(x).map(([k,v])=>[k,normalize(v)]));
 return x;
}
const copy=x=>JSON.parse(JSON.stringify(x));
for(const row of reference.cases)test('fixed FormRender: '+row.name,()=>{
 const model=copy(row.model);
 if(row.expected_error){assert.throws(()=>forms.pluginFieldProps(copy(row.props),model),row.expected_error);return;}
 if(row.kind==='props'){
  const props=forms.pluginFieldProps(copy(row.props),model);
  for(const action of row.updates||[])props[action.handler](action.value);
  assert.deepEqual({props:normalize(props),model},row.expected);
 }else{
  const scope=vue.effectScope();try{
   const result=scope.run(()=>module.exports.default.setup({node:row.node,model,view:'form'},{expose(){},emit(){}}));
   assert.deepEqual(normalize(result.render(row.node)),row.expected);
  }finally{scope.stop();}
 }
});

test('fixed PageRender uses literal props/content/events; DashboardRender literal props and named slot arrays',()=>{
 const node={component:'VCard',props:{title:'{{ missing }}','show-arrows':true,model:'literalKey'},text:0,content:[{component:'span',text:'child'}],slots:{append:[{component:'b',text:'append'}]}};
 // These expectations are source-template derived: PageRender lines77–93,
 // DashboardRender lines10–35. They are not a claimed original DOM execution.
 const scope=vue.effectScope();try{
  const page=scope.run(()=>module.exports.default.setup({node,model:{literalKey:'value'},view:'page'},{expose(){},emit(){}}));
  const rendered=normalize(page.render(node));
  assert.deepEqual(rendered.props,node.props);assert.deepEqual(Object.keys(rendered.slots),['default']);assert.equal(rendered.slots.default[0],'0');
  const dash=scope.run(()=>module.exports.default.setup({node,model:{literalKey:'value'},view:'dashboard'},{expose(){},emit(){}}));
  const dashboard=normalize(dash.render(node));assert.deepEqual(dashboard.props,node.props);assert.equal(dashboard.slots.append[0].component,'b');
 }finally{scope.stop();}
});
