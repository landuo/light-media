import test from 'node:test';
import assert from 'node:assert/strict';
import {
  directoriesFor,
  downloadDirectoryPaths,
  downloadDestination,
} from '../src/features/download-directories.ts';
const rules = [
  { id: 'base', source_path: '/downloads', library_path: '/library', media_type: 'mixed' },
  { id: 'tv', source_path: '/downloads/tv', library_path: '/library/tv', media_type: 'tv' },
  { id: 'tv-copy', source_path: '/downloads/tv/', library_path: '/other-library' },
  { id: 'empty', source_path: '' },
];
test('download directories are global source paths without a downloader or library binding', () => {
  assert.deepEqual(
    directoriesFor(rules).map((r) => r.id),
    ['base', 'tv', 'tv-copy'],
  );
  assert.deepEqual(downloadDirectoryPaths(rules), ['/downloads', '/downloads/tv']);
});
test('default downloader and automatic save location are valid selections', () => {
  assert.deepEqual(downloadDestination({}), { downloader_id: '', download_path: '' });
  assert.deepEqual(downloadDestination({ downloader_id: 'qb', download_path: '' }), {
    downloader_id: 'qb',
    download_path: '',
  });
});
test('download requests never carry organization rule or library identities', () => {
  assert.deepEqual(
    downloadDestination({
      downloader_id: 'qb',
      download_path: '/downloads/new',
      root_id: 'old-library',
      directory_id: 'old-rule',
      library_path: '/library',
    }),
    { downloader_id: 'qb', download_path: '/downloads/new' },
  );
  assert.throws(() => downloadDestination({ download_path: '../movies' }));
  assert.throws(() => downloadDestination({ download_path: '/downloads/../movies' }));
});
