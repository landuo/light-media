import { fileURLToPath } from 'node:url';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';

// Keep the identity files for old browsers, byte ranges and local preview.
async function compress(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = join(directory, entry.name);
    if (entry.isDirectory()) await compress(file);
    else if (/\.(?:js|css|html|svg|json)$/.test(file)) {
      await writeFile(file + '.gz', gzipSync(await readFile(file), { level: 9 }));
    }
  }
}
await compress(fileURLToPath(new URL('../dist', import.meta.url)));
