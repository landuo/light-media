// Offline reference extraction. No Vue app, network, user settings or plugin is run.
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import vm from 'node:vm';
import ts from '../../../web/node_modules/typescript/lib/typescript.js';

if (!process.argv[2]) throw new Error('pass the original LoggingView.vue source path');
const original = readFileSync(process.argv[2], 'utf8');
const script = original.match(/<script[^>]*>([\s\S]*?)<\/script>/)?.[1];
if (!script) throw new Error('original script missing');
const tree = ts.createSourceFile('LoggingView.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const names = new Set(['normalizeLevel', 'matchesLogFilter', 'canMergeIntoGroup']);
const functions = tree.statements.filter(ts.isFunctionDeclaration).filter((f) => names.has(f.name?.text));
if (functions.length !== names.size) throw new Error('source functions changed');
const variables = tree.statements.filter(ts.isVariableStatement).flatMap((s) => [...s.declarationList.declarations]);
const callback = (name) => {
  const init = variables.find((d) => d.name.getText(tree) === name)?.initializer;
  if (!init || !ts.isCallExpression(init) || init.expression.getText(tree) !== 'computed' || init.arguments.length !== 1)
    throw new Error('source computed changed: ' + name);
  return init.arguments[0].getText(tree);
};
const context = {
  GROUP_GAP_MS: 100,
  selectedLevel: { value: 'ALL' },
  normalizedSearchQuery: { value: '' },
  parsedLogs: { value: [] },
  groupedLogs: { value: [] },
};
const text = `(() => { ${functions.map((f) => f.getText(tree)).join('\n')}; return { normalizeLevel, grouped: ${callback('groupedLogs')}, filtered: ${callback('filteredGroups')} }; })()`;
const compiled = ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText;
const source = vm.runInNewContext(compiled, context, { timeout: 1000 });
const entry = (id, level = 'INFO', ms = 100, second = '2026-10-16 12:00:00') => ({
  id: String(id), raw: `${level}: fixture ${id} 你好`, level, appName: '', timestamp: second,
  timestampMs: ms, secondKey: second, secondDisplay: second.replaceAll('-', '/'),
  timeDisplay: '12:00:00,100', source: 'plugins', message: `fixture ${id} 你好 <literal>`,
});
const sequences = [
  [], [entry(1)], [entry(1), entry(2, 'INFO', 200)], [entry(1), entry(2, 'INFO', 201)],
  [entry(1), entry(2, 'ERROR', 120)], [entry(1), entry(2, 'INFO', 90)],
  [entry(1, 'INFO', null), entry(2, 'INFO', 120)],
  [entry(1), entry(2, 'INFO', null)],
  [entry(1, 'INFO', 100, ''), entry(2, 'INFO', 101, '')],
  [entry(1), entry(2, 'INFO', 200, '2026-10-16 12:00:01')],
  [entry(1), entry(2, 'INFO', 190), entry(3, 'INFO', 280), entry(4, 'INFO', 381)],
];
const groups = sequences.map((entries, i) => {
  context.parsedLogs.value = entries;
  return { name: 'source-group-' + i, entries, expected: source.grouped() };
});
context.parsedLogs.value = [entry(1), entry(2, 'ERROR', 120), entry(3, 'WARNING', 140)];
context.groupedLogs.value = source.grouped();
const filters = [];
for (const level of ['ALL', 'INFO', 'ERROR', 'WARNING', 'DEBUG']) {
  for (const query of ['', '  FIXTURE  ', '你好', '<literal>', 'missing', 'INFO  2026']) {
    context.selectedLevel.value = level;
    context.normalizedSearchQuery.value = query.trim().toLowerCase();
    filters.push({ level, query, groups: context.groupedLogs.value, expected: source.filtered() });
  }
}
const output = {
  source: { path: 'MoviePilot-Frontend/src/views/system/LoggingView.vue', sha256: createHash('sha256').update(original).digest('hex'), methods: [...names], computed: ['groupedLogs', 'filteredGroups'], boundary: 'Only original normalization/grouping/filter methods; Go structured-record adaptation and SSE lifecycle have separate tests.' },
  levels: ['WARN', 'warn:', ' FATAL: ', 'trace', 'INFO:', 'Warning', ' custom: ', '', '错误', 'Érr:'].map((input) => ({ input, expected: source.normalizeLevel(input) })),
  groups, filters,
};
writeFileSync(new URL('../logging-original-oracle.json', import.meta.url), JSON.stringify(output, null, 2) + '\n');
console.log(JSON.stringify({ levels: output.levels.length, groups: groups.length, filters: filters.length, source_sha256: output.source.sha256 }));
