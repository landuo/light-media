import test from 'node:test';
import assert from 'node:assert/strict';
import {
  browserRootForPath,
  commonDownloadPath,
  filterBrowserItems,
  virtualBrowserChildren,
} from '../src/features/file-browser.ts';
test('local navigation exposes authorized mounts without granting virtual parents file capabilities', () => {
  const roots = [
    { id: 'downloads', path: '/media/download', name: 'download' },
    { id: 'movies', path: '/media/movies', name: 'movies' },
    { id: 'extras', path: '/extras', name: 'extras' },
  ];
  assert.deepEqual(
    virtualBrowserChildren(roots, '/').map((item) => item.path),
    ['/extras', '/media'],
  );
  assert.deepEqual(
    virtualBrowserChildren(roots, '/media').map((item) => item.path),
    ['/media/download', '/media/movies'],
  );
  assert.equal(browserRootForPath(roots, '/media'), undefined);
  assert.equal(browserRootForPath(roots, '/media/download-other/movie.mkv'), undefined);
  assert.equal(browserRootForPath(roots, '/media/download/movie.mkv')?.id, 'downloads');
  assert.equal(
    browserRootForPath(
      [...roots, { id: 'specific', path: '/media/download/tv', name: 'tv' }],
      '/media/download/tv/show.mkv',
    )?.id,
    'specific',
  );
});
test('initial folder uses the common ancestor of configured download folders', () => {
  assert.equal(commonDownloadPath([]), '/');
  assert.equal(commonDownloadPath(['/media/download/tv/']), '/media/download/tv');
  assert.equal(
    commonDownloadPath(['/media/download/tv', '/media/download/movies']),
    '/media/download',
  );
  assert.equal(
    commonDownloadPath(['/media/download/same/end', '/media/movies/same/end']),
    '/media',
  );
  assert.equal(commonDownloadPath(['/one/download', '/two/download']), '/');
});
test('file filtering supports original wildcard and case controls without interpreting regex', () => {
  const items = [
    { name: 'Show.S01E01.mkv', path: '/one', type: 'file' },
    { name: 'show.S01E02.MKV', path: '/two', type: 'file' },
    { name: 'Show[2024].mkv', path: '/three', type: 'file' },
  ];
  assert.deepEqual(
    filterBrowserItems(items, 'show.S01E0?.mkv', true).map((item) => item.path),
    ['/one', '/two'],
  );
  assert.deepEqual(filterBrowserItems(items, 'show.S01E0?.mkv', false), []);
  assert.deepEqual(
    filterBrowserItems(items, '*[2024].mkv', true).map((item) => item.path),
    ['/three'],
  );
  assert.deepEqual(
    filterBrowserItems(items, 'S01', true).map((item) => item.path),
    ['/one', '/two'],
  );
});
