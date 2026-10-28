import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import postcss from 'postcss';
import { format, resolveConfig } from 'prettier';

const root = fileURLToPath(new URL('../', import.meta.url));
const sourcePath = path.join(root, 'node_modules/vuetify/lib/styles/main.css');
const tree = postcss.parse(await readFile(sourcePath, 'utf8'));
const scope = ':where(.plugin-surface, .plugin-dialog-surface, .plugin-log-surface)';
let rules = 0;
tree.walkRules((rule) => {
  const classes = [...rule.selector.matchAll(/\.([A-Za-z_][A-Za-z0-9_-]*)/g)].map((m) => m[1]);
  // Installed plugins supply classes at runtime. The reference includes the
  // whole Vuetify stylesheet, so a build-time source scan loses valid utilities.
  // Scope class rules to plugin surfaces without adding global element resets.
  if (!classes.length) {
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
  `/*! Generated from Vuetify 3.7.3 main.css; class utilities scoped to plugin surfaces.\n${license}\n*/\n${tree.toString()}\n`,
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
