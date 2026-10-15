import test from 'node:test';
import assert from 'node:assert/strict';
import {
  directoriesFor,
  matchingDirectory,
  downloadDestination,
} from '../src/features/download-directories.ts';
const rules = [
  { id: 'qb-base', downloader_id: 'qb', download_path: '/downloads', media_type: 'mixed' },
  { id: 'qb-tv', downloader_id: 'qb', download_path: '/downloads/tv', media_type: 'tv' },
  { id: 'tr-tv', downloader_id: 'tr', download_path: '/downloads/tv', media_type: 'tv' },
  {
    id: 'local',
    downloader_id: '',
    source_path: '/incoming',
    download_path: '',
    media_type: 'mixed',
  },
];
test('download directory selection is scoped to downloader and media type, with no pure file-monitor path exposed', () => {
  assert.deepEqual(
    directoriesFor(rules, 'qb', 'tv').map((r) => r.id),
    ['qb-base', 'qb-tv'],
  );
  assert.deepEqual(
    directoriesFor(rules, 'qb', 'movie').map((r) => r.id),
    ['qb-base'],
  );
  assert.deepEqual(directoriesFor(rules, '', 'tv'), []);
  assert.equal(matchingDirectory(rules, 'tr', '/downloads/tv/series', 'tv').id, 'tr-tv');
});
test('the most specific download directory matches descendants without matching similarly named siblings', () => {
  assert.equal(matchingDirectory(rules, 'qb', '/downloads/tv/series', 'tv').id, 'qb-tv');
  assert.equal(matchingDirectory(rules, 'qb', '/downloads/tv/', 'tv').id, 'qb-tv');
  assert.equal(matchingDirectory(rules, 'qb', '/downloads/tv-backup', 'tv').id, 'qb-base');
  assert.equal(matchingDirectory(rules, 'qb', '/downloads-other', 'tv'), undefined);
  assert.equal(matchingDirectory(rules, 'qb', '', 'tv'), undefined);
});
test('download requests carry a download directory and never accidentally reuse an old library target', () => {
  assert.deepEqual(
    downloadDestination({
      downloader_id: 'qb',
      download_path: '/downloads/new',
      root_id: 'old-library',
    }),
    {
      downloader_id: 'qb',
      download_path: '/downloads/new',
      directory_id: '',
    },
  );
  assert.throws(() => downloadDestination({ downloader_id: 'qb', download_path: '../movies' }));
  assert.throws(() =>
    downloadDestination({ downloader_id: 'qb', download_path: '/downloads/../movies' }),
  );
  assert.throws(() => downloadDestination({ downloader_id: '', download_path: '/downloads' }));
});
test('an independent file monitor is identified through the downloader path mapping, with linked rules preferred at equal paths', () => {
  const monitor = {
    id: 'pure',
    downloader_id: '',
    source_path: '/application/incoming',
    monitor_type: 'monitor',
    media_type: 'tv',
  };
  const mapping = [{ from: '/downloads', to: '/application/incoming' }];
  assert.equal(matchingDirectory([monitor], 'qb', '/downloads/Series', 'tv', mapping)?.id, 'pure');
  assert.equal(
    matchingDirectory([monitor], 'qb', '/downloads/Series', 'movie', mapping),
    undefined,
  );
  assert.equal(matchingDirectory([monitor], 'qb', '/unmapped/Series', 'tv', mapping), undefined);
  assert.equal(
    matchingDirectory([monitor, rules[0]], 'qb', '/downloads/Series', 'tv', mapping)?.id,
    'qb-base',
  );
});
