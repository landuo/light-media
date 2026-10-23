import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pluginRuntimeSupportNotice } from '../src/features/plugin-runtime-support.ts';

test('absent or malformed support never implies all platforms or storage', () => {
  for (const value of [
    undefined,
    null,
    {},
    [],
    false,
    'linux',
    { platforms: [] },
    { platforms: ['<script>'] },
    { storage_types: ['local', 'local'] },
    { platforms: Array(17).fill('linux') },
  ])
    assert.equal(pluginRuntimeSupportNotice(value), '');
});
test('declared Linux local support warns against unsupported original form examples', () => {
  const notice = pluginRuntimeSupportNotice({ platforms: ['linux'], storage_types: ['local'] });
  assert.match(notice, /Linux/);
  assert.match(notice, /本地（local）/);
  assert.match(notice, /原版表单.*不代表当前已支持/);
  assert.doesNotMatch(notice, /已验收|Windows|macOS|alipan/);
});
test('partial and future valid metadata are descriptive without invented dimensions', () => {
  assert.match(pluginRuntimeSupportNotice({ platforms: ['future_os'] }), /future_os/);
  assert.doesNotMatch(pluginRuntimeSupportNotice({ platforms: ['linux'] }), /存储 本地/);
  assert.doesNotMatch(pluginRuntimeSupportNotice({ storage_types: ['local'] }), /平台 Linux/);
});
test('both host rendering modes show the same generic escaped notice', () => {
  const source = readFileSync(
    new URL('../src/components/PluginConfigDialog.vue', import.meta.url),
    'utf8',
  );
  assert.equal((source.match(/data-testid="plugin-runtime-support"/g) || []).length, 2);
  assert.match(source, /pluginRuntimeSupportNotice\(props.plugin.runtime_support\)/);
  assert.doesNotMatch(source, /plugin_id\s*===?\s*['"]RemoveLink/);
  assert.doesNotMatch(source, /v-html=.*supportNotice/);
});
