import test from 'node:test';
import assert from 'node:assert/strict';
import { openPluginProject, pluginProjectURL } from '../src/features/plugin-project.ts';

test('project opens synchronously then resolves history metadata and normalizes raw GitHub URLs', async () => {
  const calls = [],
    navigation = [],
    popup = { opener: {}, location: { replace: (url) => navigation.push(url) }, close() {} };
  const plugin = { instance_id: 'Demo', author_url: 'https://author.invalid' };
  const running = openPluginProject(
    plugin,
    async (path) => {
      assert.equal(calls.length, 1, 'popup was opened before waiting for metadata');
      assert.equal(path, '/plugins/history/Demo?force=false');
      return { repo_url: 'https://raw.githubusercontent.com/owner/repo/main/package.json' };
    },
    (url, target) => {
      calls.push([url, target]);
      return popup;
    },
  );
  assert.equal(popup.opener, null);
  assert.equal(await running, 'https://github.com/owner/repo');
  assert.deepEqual(navigation, ['https://github.com/owner/repo']);
});

test('local or missing repository consults exact instance market identity and keeps author fallback', async () => {
  const calls = [],
    opened = [];
  const result = await openPluginProject(
    {
      instance_id: 'Demo_clone',
      repo_url: 'local://plugins',
      author_url: 'https://author.invalid/profile',
    },
    async (path) => {
      calls.push(path);
      if (path.includes('/history/')) throw new Error('history unavailable');
      return [
        { instance_id: 'Demo', repo_url: 'https://wrong-source.invalid' },
        { instance_id: 'Demo_clone', repo_url: 'https://clone.invalid' },
      ];
    },
    (url) => {
      opened.push(url);
      return null;
    },
  );
  assert.equal(result, 'https://clone.invalid/');
  assert.deepEqual(opened, ['about:blank', 'https://clone.invalid/']);
  assert.deepEqual(calls, [
    '/plugins/history/Demo_clone?force=false',
    '/plugins/market?force=false',
  ]);
  assert.equal(
    await openPluginProject(
      { instance_id: 'Demo', author_url: 'https://author.invalid' },
      async () => {
        throw new Error('offline');
      },
      () => null,
    ),
    'https://author.invalid/',
  );
});

test('unsafe or absent homepage never navigates a window and closes the empty popup', async () => {
  let closed = 0,
    navigated = 0;
  const popup = { opener: null, location: { replace: () => navigated++ }, close: () => closed++ };
  await assert.rejects(
    openPluginProject(
      { instance_id: 'Demo', author_url: 'javascript:alert(1)' },
      async () => null,
      () => popup,
    ),
    /未提供/,
  );
  assert.equal(closed, 1);
  assert.equal(navigated, 0);
  assert.equal(
    pluginProjectURL({
      repo_url: 'https://user:secret@owned.invalid',
      author_url: 'https://author.invalid',
    }),
    'https://author.invalid/',
  );
});
