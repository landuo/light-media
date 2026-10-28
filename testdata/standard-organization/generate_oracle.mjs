// Developer-only replay of the fixed frontend functions. No reference code is
// executed by the application, and no source repository is modified.
import fs from 'node:fs';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
const require=createRequire(new URL('../../web/package.json',import.meta.url));
const ts=require('typescript');
const path=process.argv[2] || '/Users/accidia/projects/MoviePilot-Frontend/src/views/plugin/PluginCardListView.vue';
const raw=fs.readFileSync(path,'utf8');
const {parse}=require('@vue/compiler-sfc');
const script=parse(raw).descriptor.scriptSetup.content;
const tree=ts.createSourceFile('fixed.ts',script,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const names=['loadPluginFolders','loadPluginOrderConfig','getFilteredFolderPlugins','saveMixedSortOrder','saveFolderPluginOrder','savePluginFolders','handleDropToFolder'];
const functions=tree.statements.filter(n=>(ts.isFunctionDeclaration(n)&&names.includes(n.name.text)) || (ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(tree)))));
if(functions.length!==names.length) throw Error('fixed function inventory changed');
const code=ts.transpileModule(functions.map(n=>n.getText(tree)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const ref=value=>({value}); const copy=value=>JSON.parse(JSON.stringify(value));
async function run(name,input) {
 const calls=[];
 const c={pluginFolders:ref(copy(input.folders||{})),folderOrder:ref([]),orderConfig:ref(copy(input.order||[])),currentFolder:ref(input.current||''),orderValueMap:ref(new Map()),mixedSortList:ref(copy(input.mixed||[])),filteredDataList:ref([]),pluginByIdMap:ref(new Map((input.plugins||[]).map(x=>[x.id,x]))),draggableFolderPlugins:ref(copy(input.sorted||[])),isDraggingSortMode:ref(true),currentDraggedPluginId:ref(input.dragged||''),installedFilter:ref(input.query||''),hasUpdateFilter:ref(!!input.updates),enabledFilter:ref(!!input.running),defaultIcon:'mdi-folder',defaultColor:'#2196F3',defaultGradient:raw.match(/const defaultGradient =\s*'([^']+)'/)[1],updateMixedSortList(){},t:x=>x,$toast:{success(){},warning(){},error(){}},console:{error(){}},api:{async get(p){if(p==='plugin/folders')return copy(input.folders);if(p==='/user/config/PluginOrder')return {data:{value:copy(input.order)}};throw Error(p)},async post(p,v){calls.push({path:p,value:copy(v)});if(input.failFolders&&p==='plugin/folders')throw Error('owned write failure')}}};
 const args=Object.keys(c);const prepare=['saveMixedSortOrder','saveFolderPluginOrder','handleDropToFolder'].includes(name)?'await loadPluginFolders();': '';
 const action=name==='getFilteredFolderPlugins'?`return ${name}(currentFolder.value)` : name==='handleDropToFolder'?`return ${name}({preventDefault(){},stopPropagation(){},currentTarget:{classList:{remove(){}}}},${JSON.stringify(input.target)})`:`return ${name}()`;
 const result=await new Function(...args,code+'\n return (async()=>{'+prepare+action+'})()')(...args.map(k=>c[k]));
 return {folders:c.pluginFolders.value,order:c.orderConfig.value,folderOrder:c.folderOrder.value,calls,...(result?{result}: {})};
}
const cases=[
 {name:'legacy-load-defaults',fn:'loadPluginFolders',input:{folders:{Legacy:['one'],Modern:{plugins:['two'],order:0,icon:'',color:'',showIcon:false,future:{n:0}}}}},
 {name:'legacy-user-order',fn:'loadPluginOrderConfig',input:{order:['two',{id:'one',order:8}]}},
 {name:'combined-quick-filters-ignore-query',fn:'getFilteredFolderPlugins',input:{folders:{Group:['one','two','three']},current:'Group',query:'not matching',updates:true,running:true,plugins:[{id:'one',plugin_name:'One',has_update:true,state:true},{id:'two',plugin_name:'Two',has_update:true,state:false},{id:'three',plugin_name:'Three',has_update:false,state:true}]}},
 {name:'root-mixed-folder-orders',fn:'saveMixedSortOrder',input:{folders:{A:{plugins:['one'],order:0},B:{plugins:[],order:1}},mixed:[{type:'folder',id:'B',data:{}},{type:'plugin',id:'two',data:{id:'two'}},{type:'folder',id:'A',data:{}}],plugins:[{id:'one'},{id:'two'}],order:[{id:'stale',type:'plugin',order:9}]}},
 {name:'filtered-folder-sort-drops-hidden-members',fn:'saveFolderPluginOrder',input:{folders:{A:{plugins:['one','hidden','two'],order:3,showIcon:false}},current:'A',sorted:[{id:'two'},{id:'one'}],order:[{id:'A',type:'folder',order:3}]}},
 {name:'drop-removes-first-other-membership',fn:'handleDropToFolder',input:{folders:{A:{plugins:['one','one','two'],order:1},B:{plugins:[],order:0}},dragged:'one',target:'B',plugins:[{id:'one'}]}}
];
const partial=copy(cases.find(c=>c.name==='root-mixed-folder-orders'));partial.name='root-sort-second-write-fails';partial.input.failFolders=true;cases.push(partial);
for(const c of cases)c.expected=await run(c.fn,c.input);
const output={fixed_commit:'2829e3b08214348f05ff2f5beae644882eccad58',source:'src/views/plugin/PluginCardListView.vue',sha256:crypto.createHash('sha256').update(raw).digest('hex'),kind:'fixed frontend function replay; synthetic values',cases};
fs.writeFileSync(new URL('./source-oracle.json',import.meta.url),JSON.stringify(output,null,2)+'\n');
console.log('fixed frontend organization replay:',cases.length,'cases');
