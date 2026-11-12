// Pure JSON audit helpers shared by the runner and node:test regression tests.
export function expectedRoots(jsonl) {
 const expected={};
 for(const line of jsonl.trim().split('\n')) {
  if(!line)continue;
  const e=JSON.parse(line);
  if(e.Package)expected[e.Package]??=[];
  if(e.Output && /^(Test|Example|Fuzz)\S+\n$/.test(e.Output))expected[e.Package].push(e.Output.trim());
 }
 return expected;
}
export function auditShard(jsonl, shard, actual, skips, unexpected) {
 for(const line of jsonl.trim().split('\n')) {
  if(!line)continue;let e;
  try{e=JSON.parse(line);}catch{unexpected.push({invalidJSON:true});continue;}
  if(e.Action==='skip')skips.push({package:e.Package,test:e.Test||null});
  if(e.Test && !e.Test.includes('/') && ['pass','fail','skip'].includes(e.Action)) {
   const key=e.Package+'/'+e.Test;
   if(actual[key])unexpected.push({duplicate:key});
   actual[key]=e.Action;
   if(e.Package!==shard.pkg || !shard.roots.includes(e.Test))unexpected.push({unexpected:key});
  }
 }
}
