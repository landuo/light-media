import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPluginRemoteAPI } from '../src/features/plugin-remote-api.ts';
const oracle = JSON.parse(
  readFileSync(
    new URL('../../testdata/standard-clone-frontend/source-oracle.json', import.meta.url),
  ),
);
test('fixed cloned JS global API literals use clone, while runtime-assembled source stays source', async () => {
  const c = oracle.cases.find((c) => c.name === 'global-JS-CSS-key-class-and-path');
  const sourceAfter = Buffer.from(c.expected_hex['remoteEntry.js'], 'hex').toString('utf8');
  const api = createPluginRemoteAPI('Owned', 'Ownedcopy', new AbortController().signal),
    seen = [];
  api.defaults.adapter = async (config) => {
    seen.push(config.url);
    return { data: '{}', status: 200, headers: {}, config };
  };
  new Function('window', sourceAfter)({ MoviePilotAPI: api });
  await new Promise(setImmediate);
  assert.deepEqual(seen, ['/api/v1/plugin/Ownedcopy/state', '/api/v1/plugin/Owned/state']);
  assert.ok(sourceAfter.includes('css__Ownedcopy__'));
  assert.ok(c.expected_hex['ownedcopy-a.css']);
  assert.equal(c.expected_hex['owned-a.css'], undefined);
});
