import { readdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { aliases } from 'vuetify/iconsets/mdi';

const root = fileURLToPath(new URL('../', import.meta.url));
const source = JSON.parse(
  await readFile(path.join(root, 'node_modules/@iconify-json/mdi/icons.json'), 'utf8'),
);
const names = new Set(Object.values(aliases).map((name) => name.replace(/^mdi-/, '')));
async function scan(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = path.join(directory, entry.name);
    if (entry.isDirectory()) await scan(name);
    else if (/\.(vue|ts|go)$/.test(name) && !name.endsWith('_test.go')) {
      const text = await readFile(name, 'utf8');
      for (const match of text.matchAll(/\bmdi[-:]([a-z0-9-]+)/g)) names.add(match[1]);
    }
  }
}
await scan(path.join(root, 'src'));
await scan(path.join(root, '../internal/plugins'));
const icons = {},
  selectedAliases = {};
function include(name) {
  if (icons[name] || selectedAliases[name]) return;
  if (source.icons[name]) icons[name] = source.icons[name];
  else if (source.aliases?.[name]) {
    selectedAliases[name] = source.aliases[name];
    include(source.aliases[name].parent);
  } else throw new Error(`Original MDI icon is unavailable: ${name}`);
}
for (const name of [...names].sort()) include(name);
const output =
  JSON.stringify(
    { prefix: 'mdi', width: 24, height: 24, icons, aliases: selectedAliases },
    null,
    2,
  ) + '\n';
const destination = path.join(root, 'src/assets/material-design-icons.json');
let previous = '';
try {
  previous = await readFile(destination, 'utf8');
} catch (error) {
  if (error.code !== 'ENOENT') throw error;
}
if (previous !== output) await writeFile(destination, output);
console.log(`Original MDI icons: ${names.size} names, ${Buffer.byteLength(output)} bytes`);
