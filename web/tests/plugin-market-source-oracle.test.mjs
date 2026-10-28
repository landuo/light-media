import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pluginMarketList,pluginMarketLabels,pluginMarketRepositoryOptions,pluginSearchMatches } from '../src/features/plugin-market.ts';
const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-market/frontend-oracle.json',import.meta.url)));
test('market filters/sorts/options/20-item prefix and separate search match actual fixed frontend AST',()=>{
 for(const row of oracle.cases){
  const cards=row.cards.map(c=>({plugin_id:c.id,instance_id:c.id,name:c.plugin_name,description:c.plugin_desc,author:c.plugin_author,plugin_label:c.plugin_label,repo_url:c.repo_url,add_time:c.add_time,installed:!!c.installed}));
  const filter={name:row.filter.name,authors:row.filter.author,labels:row.filter.label,repositories:row.filter.repo};
  const result=pluginMarketList(cards,filter,row.sort,row.stats).map(c=>c.plugin_id);
  assert.deepEqual(result,row.expected.market);
  assert.deepEqual(result.slice(0,20),row.expected.display);
  assert.deepEqual([...cards.filter(c=>c.installed),...cards.filter(c=>!c.installed)].filter(c=>pluginSearchMatches(c,row.keyword)).map(c=>c.plugin_id),row.expected.search);
  assert.deepEqual(pluginMarketRepositoryOptions(cards),row.expected.repositories);
  assert.deepEqual([...new Set(cards.flatMap(pluginMarketLabels))],row.expected.labels);
 }
});
