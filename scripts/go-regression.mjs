#!/usr/bin/env node
// No npm dependencies. Run from any directory; never uses production endpoints.
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {expectedRoots,auditShard} from './go-regression-results.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
process.chdir(root);
const mode = process.argv[2] || 'plan';
if (!['plan','preflight','run'].includes(mode)) throw Error('Usage: node scripts/go-regression.mjs [plan|preflight|run]');
const race = process.env.REGRESSION_RACE === '1';
const manifest = JSON.parse(fs.readFileSync('testdata/go-regression-fixtures.json'));
const stamp = new Date().toISOString().replace(/[:.]/g,'-');
const out = path.join(root,'work','go-regression',`${stamp}-${process.pid}`);
fs.mkdirSync(out,{recursive:true});
// Never use checkout TMPDIR: /.worktrees/ is deliberately filtered by transfer rules.
const tempBase = fs.realpathSync(os.platform() === 'win32' ? os.tmpdir() : '/tmp');
if (tempBase.split(path.sep).some(p=>p.startsWith('.'))) throw Error('Unsafe dot-component temporary root');
const tmp = fs.mkdtempSync(path.join(tempBase,'light-media-regression-'));
const env = {...process.env, TMPDIR:tmp, TMP:tmp, TEMP:tmp, GOFLAGS:'-p=2'};
const boundEnvironment={};
for(const [name,setting] of Object.entries(manifest.environment||{})) {
 const value=setting.path?path.join(root,setting.path):setting.value;
 for(const file of setting.requiredFiles||[])if(!fs.existsSync(path.join(value,file)))throw Error('Missing real fixture asset: '+name+'/'+file);
 env[name]=value;boundEnvironment[name]=value;
}
const sha = data => crypto.createHash('sha256').update(data).digest('hex');
const save = (name,data)=>fs.writeFileSync(path.join(out,name),JSON.stringify(data,null,2)+'\n');
function capture(cmd,args,timeout=1800000) {
 const r=spawnSync(cmd,args,{cwd:root,env,encoding:'utf8',maxBuffer:64*1024*1024,timeout});
 if(r.status!==0) throw Error(`${cmd} ${args.join(' ')}: ${r.error || r.stderr || r.stdout}`);
 return r.stdout;
}
function logged(name,cmd,args,timeout=2400000) {
 console.log(`${name}: ${cmd} ${args.join(' ')}`);
 const stdout=fs.openSync(path.join(out,name+'.jsonl'),'w'),stderr=fs.openSync(path.join(out,name+'.stderr'),'w');
 const start=Date.now();
 const r=spawnSync(cmd,args,{cwd:root,env,stdio:['ignore',stdout,stderr],timeout,detached:os.platform()!=='win32'});
 // Each invocation owns its process group; clean only leaked descendants of it.
 if(os.platform()!=='win32' && r.pid){try{process.kill(-r.pid,'SIGKILL');}catch(e){if(e.code!=='ESRCH')throw e;}}
 fs.closeSync(stdout);fs.closeSync(stderr);
 const result={command:[cmd,...args],status:r.status,signal:r.signal,error:r.error?.message,elapsedSeconds:(Date.now()-start)/1000};
 save(name+'.result.json',result);return result;
}
const sourceFiles=()=>capture('git',['ls-files','--cached','--others','--exclude-standard','--','*.go']).trim().split('\n').filter(Boolean).sort();
const sourceDigest=files=>sha(files.map(f=>f+'\0'+(fs.existsSync(f)?sha(fs.readFileSync(f)):'MISSING')).join('\n'));
const files=sourceFiles();
const testFiles=files.filter(f=>f.endsWith('_test.go') && !f.startsWith('testdata/') && !f.startsWith('work/'));
const dependencies={};
for(const f of testFiles){const text=fs.readFileSync(f,'utf8');for(const m of text.matchAll(/"((?:OWNED_|CATEGORYEDITOR_|SOURCE_|PLUGIN_(?:PROCESS|HOST|SOURCE)_)[A-Z_0-9]+)"/g))if(m[1].includes('BINARY') || /^OWNED_(?:ORDER|RESET|COMMAND)_/.test(m[1]))(dependencies[m[1]]??=[]).push(f);}
const mapped=new Set(manifest.fixtures.map(x=>x.env));
const unresolved=Object.keys(dependencies).filter(x=>!mapped.has(x)).map(env=>({env,reason:manifest.unresolved.find(x=>x.env===env)?.reason || 'No reviewed build recipe',files:[...new Set(dependencies[env])]}));
for(const missing of unresolved)if(!missing.env.startsWith('SOURCE_'))delete env[missing.env]; // Never inherit an unreviewed substitute.
const externalMissing=manifest.fixtures.filter(f=>f.repositoryEnv && !env[f.repositoryEnv] && !(env[f.env]&&env[f.env+'_SHA256'])).map(f=>({env:f.env,reason:'Requires explicit '+f.repositoryEnv+' pinned source checkout or binary plus '+f.env+'_SHA256'}));
unresolved.push(...externalMissing);
const sourceEnv={};
for(const f of testFiles)for(const m of fs.readFileSync(f,'utf8').matchAll(/"((?:SOURCE_|BROWSER_|CLOAKBROWSER_)[A-Z_0-9]+)"/g))(sourceEnv[m[1]]??=[]).push(f);
const provenance=Object.entries(sourceEnv).map(([name,files])=>({env:name,provided:!!env[name],files:[...new Set(files)],status:env[name]?'provided, provenance not independently attested':'not provided / unverified'}));
const linuxOnly=testFiles.filter(f=>f.endsWith('_linux_test.go'));
const meta={mode,race,dependencies,head:capture('git',['rev-parse','HEAD']).trim(),gitStatus:capture('git',['status','--short']),goVersion:capture('go',['version']).trim(),goEnv:JSON.parse(capture('go',['env','-json','GOOS','GOARCH','GOCACHE','GOMODCACHE','CGO_ENABLED','GOMOD','GOWORK','GOROOT'])),tmp,out,boundEnvironment,packageDirectories:capture('go',['list','-f','{{.ImportPath}}\t{{.Dir}}','./...']).trim().split('\n'),embeddedVCSWarning:'Go 1.26.2 only detects .git directories; nested worktree .git files may yield outer-checkout stamps. Explicit HEAD, package directories and source digest are authoritative here.',fixtureManifestSHA256:sha(fs.readFileSync('testdata/go-regression-fixtures.json')),sourceSHA256:sourceDigest(files),unresolved,provenance,linuxOnly:{status:os.platform()==='linux'?'compiled according to go build constraints':'excluded by platform, NOT passed',files:linuxOnly},limitations:['Synthetic/protocol fixtures are not live production acceptance.','Root module ./... excludes testdata and nested modules.','Optional browser, source-package and environment-gated branches are not implied covered by root-test passes.']};
save('preflight.json',meta);
console.log(`Evidence: ${out}\nTMPDIR: ${tmp}\nMapped fixtures: ${manifest.fixtures.length}; unresolved recipes: ${unresolved.length}`);
if(mode!=='plan' && externalMissing.length)throw Error('External fixture prerequisites missing; see preflight.json. No tests were started.');
if(mode==='plan'){console.log(JSON.stringify(unresolved,null,2));process.exitCode=unresolved.length?2:0;}else{
 fs.mkdirSync(path.join(tmp,'bin'));
 const builds=[];const built=new Map();
 for(const fixture of manifest.fixtures){
  let buildRoot=root, sourceBinding, prebuilt, overlayPath;
  if(fixture.repositoryEnv) {
   if(env[fixture.repositoryEnv]) {
    buildRoot=fs.realpathSync(env[fixture.repositoryEnv]);
    const revision=capture('git',['-C',buildRoot,'rev-parse','HEAD']).trim();
    if(revision!==fixture.repositoryRevision)throw Error('External fixture revision mismatch: '+fixture.env);
    if(fixture.overlayEntry){
     const entry=path.join(root,fixture.overlayEntry);
     if(!fs.existsSync(entry))throw Error('Missing retained real SDK entrypoint: '+fixture.overlayEntry);
     overlayPath=path.join(out,'overlay-'+fixture.env+'.json');
     fs.writeFileSync(overlayPath,JSON.stringify({Replace:{[path.join(buildRoot,fixture.command,'main.go')]:entry}},null,2)+'\n');
    }
    const dependencyRows=capture('go',['-C',buildRoot,'list',...(overlayPath?['-overlay',overlayPath]:[]),'-deps','-f','{{.Dir}}|{{join .GoFiles ","}}|{{join .CgoFiles ","}}|{{join .EmbedFiles ","}}',fixture.command]).trim().split('\n');
    const sources=new Set(['go.mod','go.sum']);
    for(const row of dependencyRows){const [directory,...groups]=row.split('|');if(directory===buildRoot||directory.startsWith(buildRoot+path.sep))for(const group of groups)for(const name of group.split(',').filter(Boolean))sources.add(path.relative(buildRoot,path.join(directory,name)));}
    const scopedFiles=[...sources].sort();
    const dirty=capture('git',['-C',buildRoot,'status','--porcelain','--',...scopedFiles]).trim();
    if(dirty)throw Error('External fixture dependency sources differ from pinned revision: '+fixture.env+' '+dirty);
    sourceBinding={root:buildRoot,revision,overlay:fixture.overlayEntry?{file:fixture.overlayEntry,sha256:sha(fs.readFileSync(path.join(root,fixture.overlayEntry)))}:null,files:scopedFiles.map(file=>({file,sha256:sha(fs.readFileSync(path.join(buildRoot,file)))}))};
   }else if(env[fixture.env] && env[fixture.env+'_SHA256']) {
    prebuilt=fs.realpathSync(env[fixture.env]);
    if(sha(fs.readFileSync(prebuilt))!==env[fixture.env+'_SHA256'])throw Error('Prebuilt fixture SHA256 mismatch: '+fixture.env);
    sourceBinding={prebuilt:true,sha256:env[fixture.env+'_SHA256'],status:'Caller-supplied binary hash; original source build not independently reproduced'};
   }else throw Error('Missing external prerequisite '+fixture.env+': supply '+fixture.repositoryEnv+' at pinned revision or '+fixture.env+' and '+fixture.env+'_SHA256. No substitute was built.');
  }
  const key=JSON.stringify([buildRoot,fixture.command,fixture.ldflags,fixture.overlayEntry,prebuilt]);
  let binary=prebuilt||built.get(key);
  if(!binary){binary=path.join(tmp,'bin',fixture.env.toLowerCase());const args=[...(buildRoot!==root?['-C',buildRoot]:[]),'build','-p=2',...(race?['-race']:[]),'-o',binary];if(overlayPath)args.push('-overlay',overlayPath);if(fixture.ldflags)args.push('-ldflags',fixture.ldflags);args.push(fixture.command);
   const result=logged('build-'+fixture.env,'go',args);if(result.status!==0){save('builds.json',builds);throw Error(`Fixture build failed: ${fixture.env}; see ${out}`);}built.set(key,binary);
  }
  let declaredManifest;
  if(fixture.expectedManifest || fs.readFileSync(path.join(buildRoot,fixture.command,'main.go'),'utf8').includes('--manifest')) {
   declaredManifest=JSON.parse(capture(binary,['--manifest'],10000));
   if(declaredManifest.schema_version!==1 || !declaredManifest.plugin_id || !Array.isArray(declaredManifest.methods))throw Error('Invalid real SDK manifest: '+fixture.env);
   for(const [key,value] of Object.entries(fixture.expectedManifest||{}))if(declaredManifest[key]!==value)throw Error('Unexpected external fixture identity '+fixture.env+' '+key);
   for(const method of fixture.manifestAbsentMethods||[])if(declaredManifest.methods.includes(method))throw Error('Incorrect fixture variant '+fixture.env+': '+method+' must be absent');
  }
  env[fixture.env]=binary;builds.push({...fixture,buildRecipeKey:key,binary,sha256:sha(fs.readFileSync(binary)),declaredManifest,sourceBinding,embeddedBuildMetadata:capture('go',['version','-m',binary])});
 }
 const independent=builds.filter(b=>b.repositoryEnv);
 for(let i=0;i<independent.length;i++)for(let j=0;j<i;j++){const a=fs.statSync(independent[i].binary),b=fs.statSync(independent[j].binary);if(independent[i].buildRecipeKey!==independent[j].buildRecipeKey&&((a.ino===b.ino&&a.dev===b.dev)||independent[i].sha256===independent[j].sha256))throw Error('External business workers must have independent identities');}
 save('builds.json',builds);
 save('preflight-completion.json',{completed:true,bindings:builds.length,uniqueBuilds:built.size,unresolved,finishedAt:new Date().toISOString()});
 fs.writeFileSync(path.join(out,'environment.sh'),['# Source only for owned local tests.',`export TMPDIR='${tmp}'`,"export GOFLAGS='-p=2'",...Object.entries(boundEnvironment).map(([name,value])=>`export ${name}='${value}'`),...builds.map(b=>`export ${b.env}='${b.binary}'`)].join('\n')+'\n');
 if(mode==='preflight'){process.exitCode=unresolved.length?2:0;}else{
  const listing=logged('list','go',['test','-p=2',...(race?['-race']:[]),'-json','-list','.', './...']);
  if(listing.status!==0)throw Error(`Test enumeration failed; see ${out}`);
  const expected=expectedRoots(fs.readFileSync(path.join(out,'list.jsonl'),'utf8'));
  save('expected-roots.json',expected);
  const shards=[];const size=Number(process.env.REGRESSION_SHARD_SIZE||40);if(!Number.isInteger(size)||size<1||size>200)throw Error('REGRESSION_SHARD_SIZE must be 1..200');
  for(const [pkg,tests] of Object.entries(expected)) {for(let i=0;i<Math.max(1,tests.length);i+=size)shards.push({pkg,roots:tests.slice(i,i+size)});}
  save('shards.json',shards);
  const actual={},skips=[],commands=[],unexpected=[];
  for(const [i,s] of shards.entries()){
   const name='shard-'+String(i).padStart(4,'0');const regex=s.roots.length?'^('+s.roots.join('|')+')$':'^$';
   const result=logged(name,'go',['test','-p=2',...(race?['-race']:[]),'-count=1','-json','-timeout=30m','-run',regex,s.pkg]);commands.push({name,...result});
   auditShard(fs.readFileSync(path.join(out,name+'.jsonl'),'utf8'),s,actual,skips,unexpected);
  }
  const missing=[];for(const [pkg,roots] of Object.entries(expected))for(const name of roots)if(!actual[pkg+'/'+name])missing.push(pkg+'/'+name);
  const counts={pass:0,fail:0,skip:0};for(const result of Object.values(actual))counts[result]++;
  const finalFiles=sourceFiles();
  const sourceUnchanged=JSON.stringify(files)===JSON.stringify(finalFiles)&&meta.sourceSHA256===sourceDigest(finalFiles);
  const testsPassed=sourceUnchanged&&commands.every(c=>c.status===0)&&!missing.length&&!unexpected.length&&!counts.fail;
  const summary={sourceUnchanged,testsPassed,fullyVerified:testsPassed&&!skips.length&&!unresolved.length&&os.platform()==='linux'&&provenance.length===0,expected:Object.values(expected).flat().length,covered:Object.keys(actual).length,counts,missing,unexpected,skips,unresolved,commands,limitations:meta.limitations,linuxOnly:meta.linuxOnly,provenance};
  save('summary.json',summary);console.log(JSON.stringify({testsPassed,fullyVerified:summary.fullyVerified,counts,missing:missing.length,evidence:out},null,2));
  process.exitCode=testsPassed?(summary.fullyVerified?0:2):1;
 }
}
// Evidence and owned temporary files are retained for diagnosis/reproduction.
