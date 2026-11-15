import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { loadDirectoryTransferTypes } from '../src/features/directory-transfer-types.ts';
const oracle = JSON.parse(
  readFileSync(
    new URL(
      '../../testdata/standard-module-host/transfer-types-ui-source-oracle.json',
      import.meta.url,
    ),
  ),
);
const mode = (value) => (value === 'link' ? 'hardlink' : value);
for (const row of oracle.cases)
  test('fixed DirectoryCard ' + row.input.name, async () => {
    const directory = {
      mode: mode(row.input.mode),
      _transferTypes: [{ title: '已有硬链', value: 'hardlink' }],
    };
    let calls = 0,
      errors = 0;
    const ticks = [];
    try {
      await loadDirectoryTransferTypes(
        directory,
        async () => {
          calls++;
          if (row.input.fail === calls) throw Error('owned');
          return { transtype: calls === 1 ? row.input.source : row.input.destination };
        },
        (fn) => ticks.push(fn),
      );
    } catch {
      errors++;
    }
    assert.equal(calls, row.calls.length);
    assert.equal(errors, row.errors);
    assert.equal(ticks.length, row.ticks);
    assert.equal(directory.mode, mode(row.beforeTick));
    for (const tick of ticks) tick();
    assert.equal(directory.mode, mode(row.mode));
    assert.deepEqual(
      directory._transferTypes,
      row.items.map((item) => ({ ...item, value: mode(item.value) })),
    );
  });
test('supported softlink is selectable; raw plugin API remains unchanged', async () => {
  const raw = { move: '移动', softlink: '软链', copy: '复制', link: '硬链', auto: '自动' };
  const directory = { mode: 'hardlink' };
  await loadDirectoryTransferTypes(
    directory,
    async () => ({ transtype: raw }),
    (fn) => fn(),
  );
  assert.deepEqual(
    directory._transferTypes.map((x) => x.value),
    ['move', 'softlink', 'copy', 'hardlink', 'auto'],
  );
  assert.deepEqual(Object.keys(raw), ['move', 'softlink', 'copy', 'link', 'auto']);
});
test('saved softlink survives reload without resetting other settings', async () => {
  const directory = {
    mode: 'softlink',
    source_path: '/source',
    library_path: '/library',
    scraping: false,
    monitor_type: 'downloader',
  };
  const ticks = [];
  let calls = 0;
  await loadDirectoryTransferTypes(
    directory,
    async () => {
      calls++;
      return { transtype: { link: '硬链接', softlink: '软链接', copy: '复制' } };
    },
    (fn) => ticks.push(fn),
  );
  assert.equal(calls, 2);
  assert.equal(ticks.length, 0);
  assert.equal(directory.mode, 'softlink');
  assert.equal(directory.source_path, '/source');
  assert.equal(directory.library_path, '/library');
  assert.equal(directory.scraping, false);
  assert.equal(directory.monitor_type, 'downloader');
});
test('softlink remains unavailable when one storage omits it', async () => {
  const directory = { mode: 'hardlink' };
  let calls = 0;
  await loadDirectoryTransferTypes(
    directory,
    async () => ({
      transtype: ++calls === 1 ? { link: '硬链', softlink: '软链' } : { link: '硬链' },
    }),
    (fn) => fn(),
  );
  assert.deepEqual(directory._transferTypes, [{ title: '硬链', value: 'hardlink' }]);
  assert.equal(directory.mode, 'hardlink');
});
