import fs from 'node:fs';
import crypto from 'node:crypto';
import vm from 'node:vm';
import ts from '../../web/node_modules/typescript/lib/typescript.js';

const root = '/Users/accidia/projects/MoviePilot-Frontend';
const path = root + '/src/@core/utils/formatters.ts';
const source = fs.readFileSync(path, 'utf8');
const file = ts.createSourceFile(path, source, ts.ScriptTarget.Latest);
const func = file.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === 'formatFileSize');
if (!func) throw new Error('fixed source formatter missing');
const { outputText } = ts.transpileModule(func.getText(file).replace(/^export\s+/, ''), { compilerOptions: { target: ts.ScriptTarget.ES2022 } });
const context = vm.createContext({});
vm.runInContext(outputText, context);
const cases = [null, 0, 2048, -10.5, 1e100].map((input) => ({ input, result: context.formatFileSize(input || 0) }));
const result = { frontend_commit: '2829e3b08214348f05ff2f5beae644882eccad58', kind: 'actual fixed formatFileSize pure function; DownloadingCard applies size || 0', source_sha256: crypto.createHash('sha256').update(source).digest('hex'), cases };
fs.writeFileSync(new URL('./downloader-card-source-oracle.json', import.meta.url), JSON.stringify(result, null, 2) + '\n');
console.log('fixed downloader card size cases', cases.length);
