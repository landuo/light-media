// Offline comparison of two default /api/debug/heap responses (same process).
// Rank raw sampled bytes, not extrapolated totals or RSS. Print functions only.
import {readFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';

export function snapshot(response) {
  const {heap, mem_profile_rate:rate}=response.data??{};
  if(typeof heap!=='string'||!Number.isSafeInteger(rate)||rate<=0||!heap.startsWith('heap profile:')) throw Error('Not a Go heap snapshot from /api/debug/heap');
  const entries=new Map(); let entry;
  for(const line of heap.split('\n')) {
    const row=/^(\d+):\s*(\d+)\s*\[\s*(\d+):\s*(\d+)\]\s*@\s+((?:0x[\da-fA-F]+\s*)+)$/.exec(line);
    if(row){const key=row[5].trim().split(/\s+/).join(' ');const found=entries.get(key);if(found){found.live+=Number(row[2]);found.allocated+=Number(row[4]);entry=undefined;}else{entry={live:Number(row[2]),allocated:Number(row[4]),frames:[]};entries.set(key,entry);}continue;}
    const frame=/^#\s+0x[\da-fA-F]+\s+(\S+?)(?:\+0x[\da-fA-F]+)?\s+/.exec(line);
    if(frame&&entry){entry.frames.push(frame[1]);continue;} if(!line.startsWith('#')) entry=undefined;
  }
  if(!entries.size)throw Error('No sampled stacks');return {rate,entries};
}
export function delta(a,b){
  if(a.rate!==b.rate)throw Error('Sample rates differ');
  // Historical allocated bytes should never fall at a particular stack in the same process.
  for(const [k,e] of a.entries){if((b.entries.get(k)?.allocated??0)<e.allocated)throw Error('Cumulative allocation decreased: confirm chronological order and no restart');}
  return [...b.entries].map(([key,e])=>({frames:e.frames.slice(0,12),sampledAllocDelta:e.allocated-(a.entries.get(key)?.allocated??0),sampledLiveDelta:e.live-(a.entries.get(key)?.live??0)}));
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
  if(process.argv.length!==4)throw Error('Usage: node heap-delta.mjs BEFORE.json AFTER.json');
  const [a,b]=process.argv.slice(2).map(p=>snapshot(JSON.parse(readFileSync(p,'utf8'))));
  const rows=delta(a,b);const top=k=>rows.filter(r=>r[k]>0).sort((l,r)=>r[k]-l[k]).slice(0,15);
  console.log(JSON.stringify({sampleRate:a.rate,topSampledAlloc:top('sampledAllocDelta'),topSampledLiveGrowth:top('sampledLiveDelta')},null,2));
}
