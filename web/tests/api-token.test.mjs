import test from 'node:test';
import assert from 'node:assert/strict';
import { newAPIToken } from '../src/features/api-token.ts';

test('API token generation uses cryptographic bytes and the original unbiased alphabet', () => {
  const token = newAPIToken({
    getRandomValues(bytes) {
      assert.equal(bytes.byteLength, 32);
      bytes.set(Array.from({ length: 32 }, (_, i) => i + 224));
      return bytes;
    },
  });
  assert.equal(token, 'wxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-_');
  assert.match(newAPIToken(globalThis.crypto), /^[a-zA-Z0-9_-]{32}$/);
});
