import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import vm from 'node:vm';
import ts from '../../web/node_modules/typescript/lib/typescript.js';
const commit = '2829e3b08214348f05ff2f5beae644882eccad58';
const path = 'src/components/cards/DirectoryCard.vue';
const source = execFileSync('git', ['show', `${commit}:${path}`], {cwd:'/Users/accidia/projects/MoviePilot-Frontend', encoding:'utf8'});
const start = source.indexOf('async function loadTransferTypeItems() {');
const end = source.indexOf('\n}\n', start) + 2;
if (start < 0 || end < start) throw Error('missing fixed function');
const code = ts.transpileModule(source.slice(start,end), {compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const cases=[];
for (const input of [
  {name:'intersection_and_source_order',mode:'auto',source:{copy:'来源复制',link:'来源硬链'},destination:{link:'目的硬链',copy:'目的复制'}},
  {name:'keep_current',mode:'link',source:{copy:'复制',link:'硬链'},destination:{link:'目的硬链',copy:'目的复制'}},
  {name:'empty_clear',mode:'link',source:{copy:'复制'},destination:{link:'硬链'}},
  {name:'null_preserves',mode:'link',source:null,destination:{copy:'复制'}},
  {name:'second_failure_preserves',mode:'link',source:{copy:'复制'},destination:{copy:'复制'},fail:2},
  {name:'first_failure_preserves',mode:'link',source:{copy:'复制'},destination:{copy:'复制'},fail:1},
]) {
 const directory={storage:'local',library_storage:'local',transfer_type:input.mode};
 const transferTypeItems={value:[{title:'已有硬链',value:'link'}]};
 const calls=[],ticks=[];let errors=0;
 const context={props:{directory},transferTypeItems,nextTick:fn=>ticks.push(fn),console:{log:()=>errors++},api:{get:async url=>{calls.push(url);if(input.fail===calls.length)throw Error('owned');return{transtype:calls.length===1?input.source:input.destination};}}};
 await vm.runInNewContext(code+'\nloadTransferTypeItems()',context);
 const beforeTick=directory.transfer_type;
 for(const tick of ticks)tick();
 cases.push({input,calls,errors,beforeTick,mode:directory.transfer_type,items:transferTypeItems.value,ticks:ticks.length});
}
writeFileSync(new URL('./transfer-types-ui-source-oracle.json',import.meta.url),JSON.stringify({commit,path,sha256:createHash('sha256').update(source).digest('hex'),start_line:source.slice(0,start).split('\n').length,code,cases},null,2)+'\n');
console.log(cases.length+' fixed DirectoryCard cases');
