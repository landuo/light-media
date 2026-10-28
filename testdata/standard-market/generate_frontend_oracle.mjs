// Fixed frontend AST replay; owned inputs/framework/i18n boundaries, not UI.
import ts from '../../web/node_modules/typescript/lib/typescript.js';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { writeFileSync } from 'node:fs';
const commit='2829e3b08214348f05ff2f5beae644882eccad58',path='src/views/plugin/PluginCardListView.vue';
const raw=execFileSync('git',['-C',process.argv[2],'show',`${commit}:${path}`],{encoding:'utf8'});
const script=raw.match(/<script[^>]*>([\s\S]*?)<\/script>/)[1];
const ast=ts.createSourceFile('reference.ts',script,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const funcs=['parseLocalRepoPath','handleRepoUrl','initOptions','loadMarketMore'].map(name=>ast.statements.find(n=>ts.isFunctionDeclaration(n)&&n.name.text===name).getText(ast)).join('\n');
let search,market;
function visit(node){
 if(ts.isVariableDeclaration(node)&&node.name.getText(ast)==='filterPlugins')search=node.initializer.arguments[0].getText(ast);
 if(ts.isCallExpression(node)&&node.expression.getText(ast)==='watch'&&node.arguments[0]?.getText(ast)==='[marketList, filterForm, activeSort, PluginStatistics]')market=node.arguments[1].getText(ast);
 ts.forEachChild(node,visit);
}visit(ast);
if(!search||!market)throw Error('fixed source functions missing');
const compile=code=>ts.transpileModule(code,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
const runner=new Function('cards','filter','sort','stats','keyword',compile(`
const localRepoLabel={value:'本地'};
const authorFilterOptions={value:[]},labelFilterOptions={value:[]},repoFilterOptions={value:[]};
${funcs}
const dataList={value:cards.filter(c=>c.installed)},uninstalledList={value:cards.filter(c=>!c.installed)},marketList={value:cards};
const filterForm=filter,activeSort={value:sort},PluginStatistics={value:stats},sortedUninstalledList={value:[]},displayUninstalledList={value:[]};
const isNullOrEmptyObject=v=>!v||Object.keys(v).length===0;
const keywordValue=keyword;keyword={value:keywordValue};
const search=(${search})();
(${market})();
cards.forEach(initOptions);
const all=[...displayUninstalledList.value,...sortedUninstalledList.value].map(c=>c.id),display=displayUninstalledList.value.map(c=>c.id),progression=[];
for(let i=0;i<3;i++)loadMarketMore({done:status=>progression.push({status,count:displayUninstalledList.value.length})});
return {progression,search:search.map(c=>c.id),market:all,display,authors:authorFilterOptions.value,labels:labelFilterOptions.value,repositories:repoFilterOptions.value};
`));
const cards=[
{id:'c',plugin_name:'Charlie',plugin_desc:'cleanup',plugin_author:'one',plugin_label:'tools,media',repo_url:'https://github.com/owner/repo',add_time:3,installed:true},
{id:'b',plugin_name:'Bravo',plugin_desc:'SEARCH',plugin_author:'two',plugin_label:'media',repo_url:'https://raw.githubusercontent.com/owner/second',add_time:2},
{id:'a',plugin_name:'Alpha',plugin_desc:'search',plugin_author:'one',plugin_label:'tools',repo_url:'local://a?path=%2Ftmp%2Fowned%20one',add_time:1},
{id:'d',plugin_name:'Delta',plugin_desc:'alpha tail',plugin_author:'two',plugin_label:'test label',repo_url:'local://d?path=%2Ftmp%2Fowned-two',add_time:4},
];
const filters={name:'',author:[],label:[],repo:[]};
const inputs=[
{cards,filter:filters,sort:'name',stats:{},keyword:'SEARCH'},
{cards,filter:{...filters,name:'Alpha search'},sort:'count',stats:{},keyword:'Alpha search'},
{cards,filter:{...filters,name:'search',author:['one','two'],label:['tools'],repo:['/tmp/owned one']},sort:'count',stats:{},keyword:'a'},
{cards,filter:{...filters,repo:['owner/repo','owner/second']},sort:'repo_url',stats:{a:1,b:8,c:2},keyword:''},
...['count','name','author','repo_url','add_time'].map(sort=>({cards,filter:filters,sort,stats:{c:2,b:8,a:1,d:2},keyword:'ALPHA'})),
{cards:Array.from({length:23},(_,i)=>({...cards[i%4],id:'x'+i,add_time:i})),filter:filters,sort:'count',stats:{},keyword:''},
{cards:Array.from({length:45},(_,i)=>({...cards[i%4],id:'x'+i,add_time:i})),filter:filters,sort:'count',stats:{},keyword:''},
];
const cases=inputs.map(input=>({...input,expected:runner(input.cards,input.filter,({name:'plugin_name',author:'plugin_author'}[input.sort]||input.sort),input.stats,input.keyword)}));
writeFileSync(new URL('frontend-oracle.json',import.meta.url),JSON.stringify({source_commit:commit,path,sha256:createHash('sha256').update(raw).digest('hex'),kind:'actual fixed frontend AST; owned cards; synthetic framework/network/i18n boundaries, not browser execution',cases},null,2)+'\n');
