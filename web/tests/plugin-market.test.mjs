import test from 'node:test';
import assert from 'node:assert/strict';
import { pluginMarketList } from '../src/features/plugin-market.ts';
const cards = [
  { plugin_id: 'c', name: 'Charlie', description: 'cleanup', author: 'one', plugin_label: 'tools,media', repo_url: 'repo1', add_time: 3 },
  { plugin_id: 'b', name: 'Bravo', description: 'SEARCH', author: 'two', plugin_label: 'media', repo_url: 'repo2', add_time: 2 },
  { plugin_id: 'a', name: 'Alpha', description: 'search', author: 'one', plugin_label: 'tools', repo_url: 'repo1', add_time: 1 },
];
const filters = { name: '', authors: [], labels: [], repositories: [] };
test('market dimensions intersect, multiple selections union, source statistics gate all five sorts', () => {
  assert.deepEqual(pluginMarketList(cards, { ...filters, name: 'search', authors: ['one', 'two'], labels: ['tools'], repositories: ['repo1'] }, 'count', {}).map(x => x.plugin_id), ['a']);
  assert.deepEqual(pluginMarketList(cards, filters, 'name', {}).map(x => x.plugin_id), ['c', 'b', 'a']);
  const counts = { c: 2, b: 8, a: 1 };
  assert.deepEqual(pluginMarketList(cards, filters, 'count', counts).map(x => x.plugin_id), ['b', 'c', 'a']);
  assert.deepEqual(pluginMarketList(cards, filters, 'name', counts).map(x => x.plugin_id), ['a', 'b', 'c']);
  assert.deepEqual(pluginMarketList(cards, filters, 'add_time', counts).map(x => x.plugin_id), ['a', 'b', 'c']);
  assert.deepEqual(cards.map(x => x.plugin_id), ['c', 'b', 'a'], 'sorting is display-only');
});
