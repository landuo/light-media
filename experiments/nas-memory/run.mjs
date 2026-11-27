// node run.mjs /path/to/server /path/to/seed NEW_RESULT.json
import {spawn, execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {mkdtempSync, rmSync, readFileSync, writeFileSync} from 'node:fs';
import {createServer} from 'node:net';
import {join, resolve} from 'node:path';
import {tmpdir, release} from 'node:os';
import {setTimeout as sleep} from 'node:timers/promises';
import assert from 'node:assert/strict';

if (process.argv.length !== 5) throw Error('usage: node run.mjs SERVER SEED NEW_RESULT.json');
const [binary, seed, out] = process.argv.slice(2).map(x => resolve(x));
const sha = f => createHash('sha256').update(readFileSync(f)).digest('hex');
const profile=process.env.NAS_MEMORY_PROFILE??'short';
assert.ok(['short','long'].includes(profile),'NAS_MEMORY_PROFILE must be short or long');
const params=profile==='long'?{rounds:8,requests:500,gapMs:2,idleTailMs:125000}:{rounds:4,requests:100,gapMs:20,idleTailMs:20000};
const result = {profile,params,binary, sha256: sha(binary), seedSha256: sha(seed), os: `${process.platform}/${process.arch} ${release()}`, start: new Date().toISOString(), samples: [], phases: []};
const tmp = mkdtempSync(join(tmpdir(), 'lm-nas-mem-')), data = join(tmp, 'data');
const env = {HOME: tmp, TMPDIR: tmp, PATH: '/usr/bin:/bin', LIGHT_MEDIA_AUTO_UPDATE:'off', LIGHT_MEDIA_STARTUP_RECONCILE:'off', PLUGIN_MARKET:'', BIG_MEMORY_MODE:'false', GOMAXPROCS:'4', HTTP_PROXY:'http://127.0.0.1:1', HTTPS_PROXY:'http://127.0.0.1:1', NO_PROXY:'127.0.0.1'};
const states = ['baseline','imported','observing','needs_review','error'];
const expected = Object.fromEntries(Array.from({length:4}, (_,i) => {
  const id = `rule-${i}`;
  return [id, {counts:Object.fromEntries(states.map(s=>[s,42])), pending:[209,208,207,205,204].map(j=>({file:`${id}-${j}.mkv`, state:states[j%5], reason:'synthetic'}))}];
}));
let child, closed, timer, lastGC, active, samplingError;
const interrupt = () => child?.kill('SIGTERM');
process.once('SIGINT',interrupt); process.once('SIGTERM',interrupt);
try {
  execFileSync(seed, [data], {env, cwd: tmp});
  const portReservation = createServer();
  await new Promise((ok,no)=>portReservation.once('error',no).listen(0,'127.0.0.1',ok));
  const addr = `127.0.0.1:${portReservation.address().port}`;
  await new Promise(ok=>portReservation.close(ok));
  child = spawn(binary, ['--safe','--data-dir',data,'--listen',addr], {env,cwd:tmp,stdio:['ignore','pipe','ignore']});
  closed = new Promise((ok,no)=>{child.once('error',no);child.once('close',ok)});
  let stdout=''; child.stdout.on('data',buf=>{stdout=(stdout+buf).slice(-65536)});
  async function check() {
    const r=await fetch(`http://${addr}/healthz`,{signal:AbortSignal.timeout(5000),redirect:'error'});
    if(r.status!==200) throw Error(`health status ${r.status}`);
    const {data:v}=await r.json(); assert.equal(v.status,'ok'); assert.deepEqual(v.directory_states,expected); assert.equal(v.plugins_loaded,0); assert.ok(Number.isInteger(v.memory.num_gc)); return v.memory.num_gc;
  }
  const started=performance.now();
  while(!stdout.includes(`light-media listening at ${addr}`)) {if(performance.now()-started>10000 || child.exitCode!==null) throw Error('own listener did not start'); await sleep(25)}
  while(true){try{lastGC=await check();break}catch(e){if(e.message!=='health status 503' || performance.now()-started>10000) throw e; await sleep(30)}}
  result.expected=expected; result.preGC=lastGC;
  const sample=()=>{ if(!active || samplingError) return; try {const rss=Number(execFileSync('/bin/ps',['-o','rss=','-p',String(child.pid)],{encoding:'utf8'}).trim());assert.ok(rss>0);result.samples.push({phase:active,ms:Math.round(performance.now()-started),rssKiB:rss})}catch(e){samplingError=e}};
  timer=setInterval(sample,100);
  async function phase(name, n, idleMs=0){
    active=name; const t=performance.now(), startIndex=result.samples.length, gc0=lastGC;
    sample();
    for(let i=0;i<n;i++){await check(); await sleep(params.gapMs)}
    if(idleMs) await sleep(idleMs);
    sample();active=null;if(samplingError) throw samplingError;
    const elapsedMs=Math.round(performance.now()-t); lastGC=await check();
    const rss=result.samples.slice(startIndex).map(s=>s.rssKiB).sort((a,b)=>a-b);
    const p={name,completed:n,elapsedMs,nRSS:rss.length,peakKiB:rss.at(-1),medianKiB:(rss[(rss.length-1)>>1]+rss[rss.length>>1])/2,gc0,gc1:lastGC,deltaGC:lastGC-gc0};result.phases.push(p); console.error(JSON.stringify(p));
  }
  await phase('idle-initial',0,3000);
  for(let i=1;i<=params.rounds;i++) await phase(`load-${i}`,params.requests);
  await phase('idle-tail',0,params.idleTailMs);
  result.postGC=lastGC;
} finally {
  clearInterval(timer);
  if(child){child.kill('SIGTERM');const kill=setTimeout(()=>child.kill('SIGKILL'),8000);try{await closed}finally{clearTimeout(kill)}}
  rmSync(tmp,{recursive:true,force:true});
}
writeFileSync(out,JSON.stringify(result,null,2)+'\n',{flag:'wx'});
