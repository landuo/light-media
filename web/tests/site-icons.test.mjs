import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import ts from 'typescript';
const source = fs.readFileSync(new URL('../src/features/site-icons.ts', import.meta.url), 'utf8');
const mod = { exports: {} };
const code = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
new Function('require', 'exports', code)(
  (name) => ({ __esModule: true, default: name }),
  mod.exports,
);
const { siteIconURL, defaultIcon } = mod.exports;
test('known sites and aliases use original icons; fallback never carries user info, path or query', () => {
  assert.match(siteIconURL('https://mua.xloli.cc'), /xloli\.cc\.ico$/);
  assert.match(siteIconURL('https://api.m-team.cc'), /m-team\.cc\.png$/);
  assert.match(siteIconURL('https://kp.m-team.cc'), /m-team\.cc\.png$/);
  assert.match(siteIconURL('https://ubits.club'), /ubits\.club\.ico$/);
  assert.equal(
    siteIconURL('https://unrelated.example.org/path?passkey=must-not-send'),
    'https://unrelated.example.org/favicon.ico',
  );
  assert.equal(
    siteIconURL('https://mua.xloli.cc.evil.org'),
    'https://mua.xloli.cc.evil.org/favicon.ico',
  );
  for (const value of [
    'javascript:alert(1)',
    'http://mua.xloli.cc',
    'https://user:password@example.org',
    'invalid',
  ])
    assert.equal(siteIconURL(value), defaultIcon);
  const files = fs.readdirSync(new URL('../src/assets/sites', import.meta.url));
  assert.equal(files.length, 18);
  for (const file of files) {
    const b = fs.readFileSync(new URL('../src/assets/sites/' + file, import.meta.url));
    assert.ok(b.length > 100 && b.length < 512 * 1024);
    if (file.endsWith('.ico')) assert.deepEqual([...b.subarray(0, 4)], [0, 0, 1, 0]);
  }
});
