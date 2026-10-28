import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
const require=createRequire(new URL('../../web/package.json',import.meta.url));
const ts=require('typescript'), vue=require('vue');
const commit='2829e3b08214348f05ff2f5beae644882eccad58';
const read=(path)=>execFileSync('git',['show',commit+':src/'+path],{cwd:'/Users/accidia/projects/MoviePilot-Frontend',encoding:'utf8'}).match(/<script[^>]*>([\s\S]*?)<\/script>/)[1];
const parse=(text)=>ts.createSourceFile('fixed.ts',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const js=(text)=>ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
const copy=(value)=>JSON.parse(JSON.stringify(value));
const r=read('pages/recommend.vue');
const rf=parse(r).statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='loadExtraRecommendSources').getText();
const extras=[{name:'Owned one',api_path:'plugin/Owned/feed?kind=a',type:'电影'},{name:'Duplicate path',api_path:'plugin/Owned/feed?kind=a',type:'电视剧'},{name:'Same title',api_path:'plugin/Owned/feed2',type:'自定义类别'},{name:'Builtin duplicate',api_path:'recommend/tmdb_movies',type:'电影'},{name:'TMDB热门电影',api_path:'plugin/Owned/same-name',type:'电视剧'}];
const base=[{apipath:'recommend/tmdb_movies',title:'TMDB热门电影',type:'电影'}],list=copy(base);
const enableNode=parse(r).statements.find(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(x=>x.name.getText()==='enableConfig')).getText();
const initialEnabled=new Function('ref','viewList',js(enableNode)+';return enableConfig.value;')(vue.ref,copy(base));
const fn=new Function('api','extraRecommendSources','viewList',js(rf)+';return loadExtraRecommendSources;')({get:async()=>copy(extras)},vue.ref([]),list);await fn();
const d=read('pages/discover.vue');const df=parse(d).statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='loadExtraDiscoverSources').getText();
const dextra=[{name:'Owned extra',mediaid_prefix:'owned',api_path:'plugin/Owned/feed',filter_params:{},filter_ui:[]},{name:'Dup',mediaid_prefix:'owned',api_path:'other',filter_ui:[]},{name:'Builtin',mediaid_prefix:'douban',api_path:'other',filter_ui:[]}];const dbase=[{name:'豆瓣',mediaid_prefix:'douban'}],tabs=vue.ref(copy(dbase));await new Function('api','extraDiscoverSources','discoverTabs','itemColors','getItemColor',js(df)+';return loadExtraDiscoverSources;')({get:async()=>copy(dextra)},vue.ref([]),tabs,vue.ref({}),()=> 'owned-color')();
const f=read('views/discover/ExtraSourceView.vue');const st=parse(f).statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(x=>['default_params','filterParams','previousParams','currentKey'].includes(x.name.getText()))||ts.isExpressionStatement(n)&&ts.isCallExpression(n.expression)&&n.expression.expression.getText()==='watch').map(n=>n.getText()).join('\n');
const cases=[
 {name:'dependency-null',initial:{genre:'all',region:null},depends:{region:['genre']},change:{genre:'action'}},
 {name:'truthy-default-restores-after-clear',initial:{genre:'all',region:'CN'},depends:{region:['genre']},change:{genre:'action'}},
 {name:'key-order-reversed',initial:{region:'CN',genre:'all'},depends:{region:['genre']},change:{genre:'action'}},
 {name:'falsey-restores-default',initial:{genre:'all',region:null},depends:null,change:{genre:''}},
 {name:'array-reference-comparison',initial:{genres:['all'],region:'CN',tag:''},depends:{region:['genres']},change:{tag:'updated'}},
];
for(const c of cases){const scope=vue.effectScope();const state=scope.run(()=>new Function('props','cloneDeep','reactive','ref','watch',js(st)+';return {filterParams,currentKey};')({source:{filter_params:copy(c.initial),depends:c.depends}},copy,vue.reactive,vue.ref,vue.watch));Object.assign(state.filterParams,c.change);try{await vue.nextTick();}catch(error){c.error=String(error);}c.expected=copy(state.filterParams);c.reset_count=state.currentKey.value;scope.stop();}
const m=read('views/discover/MediaCardListView.vue');const getParams=parse(m).statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='getParams').getText();const params=[null,{genre:'科幻'},{page:7,genre:''},{page:'7',list:'ordinary',title:['Old title','Owned title']}].map(value=>({input:value,expected:new Function('page','props',js(getParams)+';return getParams();')({value:2},{params:value})}));
const out={fixed_frontend:commit,kind:'executed exact fixed TS functions and Vue reactive watcher; owned JSON models',recommend:{input:extras,base,expected:list,initial_enabled_by_title:initialEnabled,visible_after_append:list.filter(row=>initialEnabled[row.title])},discover:{input:dextra,base:dbase,expected:copy(tabs.value)},filters:cases,pagination:params};fs.writeFileSync(new URL('./source-oracle.json',import.meta.url),JSON.stringify(out,null,2)+'\n');console.log('fixed extra source UI oracle: recommend/discover +'+cases.length+' watch cases +'+params.length+' page parameter cases');
