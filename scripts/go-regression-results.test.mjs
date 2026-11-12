import test from 'node:test';
import assert from 'node:assert/strict';
import {expectedRoots,auditShard} from './go-regression-results.mjs';
const lines=rows=>rows.map(x=>JSON.stringify(x)).join('\n');
test('listing includes tests examples and fuzz seeds, not benchmarks or noise',()=>{
 assert.deepEqual(expectedRoots(lines([{Package:'p',Output:'TestAlpha\n'},{Package:'p',Output:'ExampleAlpha\n'},{Package:'p',Output:'FuzzAlpha\n'},{Package:'p',Output:'BenchmarkAlpha\n'},{Package:'empty',Action:'skip'}])),{p:['TestAlpha','ExampleAlpha','FuzzAlpha'],empty:[]});
});
test('root failures and nested skips are preserved; missing roots remain absent',()=>{
 const actual={},skips=[],bad=[];
 auditShard(lines([{Package:'p',Test:'TestA/sub',Action:'skip'},{Package:'p',Test:'TestA',Action:'fail'}]),{pkg:'p',roots:['TestA','TestB']},actual,skips,bad);
 assert.deepEqual(actual,{'p/TestA':'fail'});assert.equal(actual['p/TestB'],undefined);assert.equal(skips.length,1);assert.equal(bad.length,0);
});
test('duplicates unexpected package/roots and malformed JSON cannot pass audit',()=>{
 const actual={},skips=[],bad=[];
 auditShard(lines([{Package:'p',Test:'TestA',Action:'pass'},{Package:'p',Test:'TestA',Action:'pass'},{Package:'other',Test:'TestA',Action:'pass'}])+'\nBROKEN',{pkg:'p',roots:['TestA']},actual,skips,bad);
 assert.equal(bad.length,3);
});
