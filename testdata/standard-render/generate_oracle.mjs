// Fixed frontend function replay only; original checkout remains read-only.
import fs from 'node:fs';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
const require=createRequire(new URL('../../web/package.json',import.meta.url));
const ts=require('typescript'), {parse}=require('@vue/compiler-sfc'), vue=require('vue');
const root=process.argv[2]||'/Users/accidia/projects/MoviePilot-Frontend/src/components/render';
const raw=fs.readFileSync(root+'/FormRender.vue','utf8');
const script=parse(raw).descriptor.scriptSetup.content;
const tree=ts.createSourceFile('fixed.ts',script,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const names=['parseProps','renderSlotContent','renderComponent'];
const selected=tree.statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(tree))));
if(selected.length!==3)throw Error('fixed source function inventory changed');
const code=ts.transpileModule(selected.map(n=>n.getText(tree)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const copy=x=>JSON.parse(JSON.stringify(x));
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
const replay=new Function('h','resolveComponent', '\"use strict\";'+code+';return {parseProps,renderComponent}')(vue.h,x=>x.startsWith('V')?{name:x}:x);
const cases=[
 {name:'model-aliases-and-updates',kind:'props',model:{text:'start',count:2,choice:'A'},props:{modelvalue:'text','v-model':'count','model:selection':'choice',label:'text'},updates:[{handler:'onUpdate:value',value:'changed'},{handler:'onUpdate:modelValue',value:4},{handler:'onUpdate:selection',value:'B'}]},
 {name:'literal-unicode-and-punctuation-keys',kind:'props',model:{'中文 标题':'原值','custom[password]':'brackets','custom.constructor':'literal dotted','7':'seven','null':'nullable'},props:{model:'中文 标题','model:selection:ignored':'custom[password]',label:'custom.constructor',modelvalue:7,'model:nullable':null},updates:[{handler:'onUpdate:modelValue',value:'新值'},{handler:'onUpdate:selection',value:'edited'},{handler:'onUpdate:value',value:'seven edited'},{handler:'onUpdate:nullable',value:'null edited'}]},
 {name:'native-style-keeps-css-props-and-complex-author-rules',kind:'render',model:{},node:{component:'style',props:{media:'screen'},text:'/* owned */ @media (min-width:300px) { .owned > b:is(.probe) { --owned:red; transform:translateX(3px); color:var(--owned); padding-block:5px } }'}},
 {name:'native-style-html-keeps-form-inner-component-layer',kind:'render',model:{},node:{component:'style',props:{media:'screen'},html:'.owned-html-probe { color:rgb(12,34,56) }'}},
 {name:'missing-show-variable-propagates-source-reference-error',kind:'props',model:{},props:{show:'{{ owned_missing_visible }}'}},
 {name:'show-aliases-follow-property-insertion-order',kind:'props',model:{visible:true,hidden:false},props:{show:'{{ visible }}','v-show':'{{ hidden }}'}},
 {name:'ordinary-show-prefixed-property',kind:'props',model:{title:'owned label'},props:{'show-arrows':true,label:'title',color:'primary'}},
 {name:'show-before-later-style',kind:'props',model:{enabled:false},props:{show:'{{ enabled }}',style:{color:'red'}}},
 {name:'event-function-factory',kind:'props',model:{total:3},props:{onClick:'(function(){return e => { model.total += e }})()'},updates:[{handler:'onClick',value:5}]},
 {name:'falsey-form-text-falls-through-content',kind:'render',model:{},node:{component:'VCard',text:0,content:[{component:'span',text:'owned fallback'}]}},
 {name:'form-rich-html-retains-component-layer',kind:'render',model:{},node:{component:'VCard',props:{class:'owned-card'},html:'<b>owned rich text</b>',content:[{component:'span',text:'ignored'}]}},
 {name:'form-slot-object-array-and-default-precedence',kind:'render',model:{key:'owned'},node:{component:'VTextField',props:{model:'key'},slots:{append:{component:'span',text:'suffix'},prepend:[{component:'b',text:'prefix'}],default:{component:'span',text:'overridden'}},content:[{component:'span',text:'default content'}]}}
];
const rich=JSON.parse(fs.readFileSync(new URL('../../cmd/owned-standard-fixture/rich-form.json',import.meta.url),'utf8'));
cases.push({name:'owned-rich-recursive-browser-form',kind:'render',model:rich[1],node:rich[0][0]});
for(const row of cases){const model=copy(row.model);try{if(row.kind==='props'){const props=replay.parseProps(copy(row.props),model);for(const action of row.updates||[])props[action.handler](action.value);row.expected={props:normalize(props),model};}else row.expected=normalize(replay.renderComponent(copy(row.node),model));}catch(error){row.expected_error={name:error.name,message:error.message};}}
// Actual fixed PageRender watchEffect, including the source's retained
// listeners when an events key disappears on a subsequent page response.
const pageRaw=fs.readFileSync(root+'/PageRender.vue','utf8');
const pageScript=parse(pageRaw).descriptor.scriptSetup.content;
const pageTree=ts.createSourceFile('fixed-page.ts',pageScript,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const pageNodes=pageTree.statements.filter(n=>(ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>d.name.getText(pageTree)==='componentEvents'))||(ts.isExpressionStatement(n)&&ts.isCallExpression(n.expression)&&n.expression.expression.getText(pageTree)==='watchEffect'));
if(pageNodes.length!==2)throw Error('fixed page event setup inventory changed');
const pageCode=ts.transpileModule(pageNodes.map(n=>n.getText(pageTree)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const pageEvent={steps:[{events:{click:{api:'plugin/Owned/one',method:'GET',params:{first:true}}}},{events:{}},{events:{click:{api:'plugin/Owned/two',method:'POST',params:{second:true}}}}],calls:[]};
const pageProps={config:{}},pageCalls=[];let runPageEffect;
const pageReplay=new Function('reactive','watchEffect','props','isNullOrEmptyObject','commonAction',pageCode+';return componentEvents')(x=>x,fn=>{runPageEffect=fn},pageProps,x=>!x||Object.keys(x).length===0,async(...args)=>pageCalls.push(args));
for(const step of pageEvent.steps){pageProps.config=copy(step);runPageEffect();await pageReplay.click();pageEvent.calls.push(copy(pageCalls));}
const sources=Object.fromEntries(['FormRender.vue','PageRender.vue','DashboardRender.vue'].map(n=>[n,crypto.createHash('sha256').update(fs.readFileSync(root+'/'+n)).digest('hex')]));
fs.writeFileSync(new URL('./source-oracle.json',import.meta.url),JSON.stringify({commit:'2829e3b08214348f05ff2f5beae644882eccad58',kind:'thirteen actual fixed FormRender function cases; synthetic models and inert HTML',sources,cases,page_event_cases:pageEvent},null,2)+'\n');
console.log('fixed FormRender function cases',cases.length);
