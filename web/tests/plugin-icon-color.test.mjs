import test from 'node:test';import assert from 'node:assert/strict';import {readFileSync} from 'node:fs';
import {createPluginIconColorReader} from '../src/features/plugin-icon-color.ts';
test('fixed ColorThief2.6 quality20 pixel quantization and fallback use the actual browser library',async t=>{
 const previous=globalThis.document;t.after(()=>{if(previous===undefined)delete globalThis.document;else globalThis.document=previous});
 globalThis.document={createElement(){let image;return {getContext(){return {drawImage(x){image=x},getImageData(){return {data:new Uint8ClampedArray(Array(400).fill(image.rgba).flat())}}}}}}};
 const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-navigation/icon-color-oracle.json',import.meta.url)));
 const color=createPluginIconColorReader();for(const c of oracle.cases)assert.equal(await color({src:c.name,naturalWidth:20,naturalHeight:20,rgba:c.rgba}),c.expected,c.name);
});
test('fixed image cache shares promises/results, evicts first at100 and keeps original quality/fallback',async()=>{
 const calls=[];const color=createPluginIconColorReader((image,quality)=>{calls.push({src:image.src,quality});if(image.fail)throw Error('owned canvas unavailable');return [1,2,3]});
 assert.equal(await color(null),'#28A9E1');assert.equal(await color({src:'fail',fail:true},{fallback:'#abcdef'}),'#abcdef');
 assert.equal(await color({src:'fail'},{quality:1}),'#abcdef','same URL caches first rejected fallback');
 assert.equal(await color({src:'first'}),'#010203');await color({src:'first'},{quality:1});assert.equal(calls.filter(x=>x.src==='first').length,1);assert.equal(calls.at(-1).quality,20);
 for(let i=0;i<100;i++)await color({src:'owned'+i});await color({src:'first'},{quality:3});assert.equal(calls.at(-1).quality,3);assert.equal(calls.filter(x=>x.src==='first').length,2);
});
