import { readdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import postcss from 'postcss';
import { format, resolveConfig } from 'prettier';

const root = fileURLToPath(new URL('../', import.meta.url));
const sourcePath = path.join(root, 'node_modules/vuetify/lib/styles/main.css');
let corpus = '';
async function scan(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = path.join(directory, entry.name);
    if (entry.isDirectory()) await scan(name);
    else if (
      (/\.(vue|ts|go)$/.test(name) ||
        (name.includes('/frontend/assets/') && name.endsWith('.js'))) &&
      !name.endsWith('_test.go')
    )
      corpus += '\n' + (await readFile(name, 'utf8'));
  }
}
await scan(path.join(root, 'src'));
await scan(path.join(root, '../internal/plugins'));
const tree = postcss.parse(await readFile(sourcePath, 'utf8'));
const scope = ':where(.plugin-surface, .plugin-dialog-surface, .plugin-log-surface)';
let rules = 0;
tree.walkRules((rule) => {
  const classes = [...rule.selector.matchAll(/\.([A-Za-z_][A-Za-z0-9_-]*)/g)].map((m) => m[1]);
  // Keep the installed component version's actual utility declarations; no
  // element resets, global theme defaults, unrelated selectors or new palette.
  if (!classes.length || !classes.some((name) => corpus.includes(name))) {
    rule.remove();
    return;
  }
  rule.selectors = rule.selectors.flatMap((selector) => [
    `${scope} :is(${selector})`,
    `${scope}:is(${selector})`,
  ]);
  // The reference project's Vuetify variables override Roboto with Inter and
  // its CJK fallbacks. Inherit that existing app font, retaining source sizes.
  rule.walkDecls('font-family', (decl) => {
    decl.value = 'inherit';
  });
  rules++;
});
tree.walkAtRules((node) => {
  if (node.name.includes('keyframes') || (node.nodes && !node.nodes.length)) node.remove();
});
tree.walkComments((comment) => comment.remove());
const license = await readFile(path.join(root, 'node_modules/vuetify/LICENSE.md'), 'utf8');
const destination = path.join(root, 'src/assets/plugin-utilities.css');
const output = await format(
  `/*! Generated from Vuetify 3.7.3 main.css; used utilities scoped to plugin surfaces.\n${license}\n*/\n${tree.toString()}\n`,
  { ...(await resolveConfig(destination)), parser: 'css' },
);
let previous = '';
try {
  previous = await readFile(destination, 'utf8');
} catch (e) {
  if (e.code !== 'ENOENT') throw e;
}
if (previous !== output) await writeFile(destination, output);
console.log(`Original plugin utilities: ${rules} rules, ${Buffer.byteLength(output)} bytes`);
