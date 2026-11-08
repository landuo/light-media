import test from 'node:test';
import assert from 'node:assert/strict';
import { image, ui } from '../src/api.ts';

test('catalog images stay on the authenticated same-origin proxy', () => {
  assert.equal(
    image('/sample-poster.jpg'),
    '/api/catalog/image?path=%2Fsample-poster.jpg&size=w342',
  );
  assert.equal(image('/sample.jpg', 'w1280'), '/api/catalog/image?path=%2Fsample.jpg&size=w1280');
  assert.equal(image('/sample.jpg', 'original'), '/api/catalog/image?path=%2Fsample.jpg&size=w342');
  assert.equal(
    image('https://image.tmdb.org/t/p/original/sample.jpg', 'w1280'),
    '/api/catalog/image?path=%2Fsample.jpg&size=w1280',
  );
  for (const path of [
    'https://example.invalid/a.jpg',
    'https://example.invalid/t/p/original/a.jpg',
    'https://image.tmdb.org@elsewhere.invalid/t/p/original/a.jpg',
    'https://image.tmdb.org/t/p/original/../a.jpg',
    'https://image.tmdb.org/t/p/original/a.jpg?unexpected=value',
    '//elsewhere.invalid/a.jpg',
    '/../a.jpg',
    '',
  ]) {
    assert.equal(image(path), '');
  }
});

test('catalog images accept the configured TMDB image host', () => {
  const previous = ui.settings;
  try {
    ui.settings = { tmdb_image_domain: 'image.example.invalid' };
    assert.equal(
      image('https://image.example.invalid/t/p/w500/sample.jpg'),
      '/api/catalog/image?path=%2Fsample.jpg&size=w342',
    );
  } finally {
    ui.settings = previous;
  }
});
