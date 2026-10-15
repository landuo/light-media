import test from 'node:test';
import assert from 'node:assert/strict';
import { image } from '../src/api.ts';

test('catalog images stay on the authenticated same-origin proxy', () => {
  assert.equal(
    image('/sample-poster.jpg'),
    '/api/catalog/image?path=%2Fsample-poster.jpg&size=w342',
  );
  assert.equal(image('/sample.jpg', 'w1280'), '/api/catalog/image?path=%2Fsample.jpg&size=w1280');
  assert.equal(image('/sample.jpg', 'original'), '/api/catalog/image?path=%2Fsample.jpg&size=w342');
  for (const path of [
    'https://example.invalid/a.jpg',
    '//elsewhere.invalid/a.jpg',
    '/../a.jpg',
    '',
  ]) {
    assert.equal(image(path), '');
  }
});
