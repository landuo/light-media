// Development-only fixed source replay; the original app/config is never loaded.
import fs from 'node:fs';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
const require=createRequire(new URL('../../web/package.json',import.meta.url));const ts=require('typescript');const {parse}=require('@vue/compiler-sfc');
const path=process.argv[2]||'/Users/accidia/projects/MoviePilot-Frontend/src/views/system/LoggingView.vue';
const original=fs.readFileSync(path,'utf8'), script=parse(original).descriptor.scriptSetup.content;
const tree=ts.createSourceFile('fixed.ts',script,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const names=['normalizeLevel','stripAnsi','extractTimestamp','getTimestampMs','getSecondKey','getSecondDisplay','getTimeDisplay','extractMessage','createLogEntry','parsePythonStyleLog','parseBracketStyleLog','parseTimestampFirstLog','parseInlineLevelLog','parseLogLine','flushBuffer'];
const functions=tree.statements.filter(n=>ts.isFunctionDeclaration(n)&&names.includes(n.name.text));
if(functions.length!==names.length)throw Error('fixed logging function inventory changed');
const constants=tree.statements.filter(ts.isVariableStatement).flatMap(n=>n.declarationList.declarations).filter(n=>['DEFAULT_LEVELS','MAX_LOG_LINES','ANSI_PATTERN','TIMESTAMP_PATTERN'].includes(n.name.getText(tree))).map(n=>'const '+n.getText(tree)+';').join('\n');
const code=ts.transpileModule(constants+'\n'+functions.map(n=>n.getText(tree)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
function run(chunks){
 const buffer=[...chunks],parsedLogs={value:[]},followTail={value:false},pendingLogCount={value:0};
 const invoke=new Function('buffer','parsedLogs','followTail','pendingLogCount',code+'\n let logSequence=0,timeoutId=null; function isNearBottom(){return false} function enableFollow(){};flushBuffer();return parsedLogs.value;');
 return {entries:invoke(buffer,parsedLogs,followTail,pendingLogCount),pending:pendingLogCount.value};
}
const cases=[
 {name:'traceback-blank-ansi-and-embedded-severity',messages:['first\nTraceback (owned):\n  detail <literal>\n\n\u001b[31mERROR: 2026-10-26 12:00:00,005 callback - owned failed\u001b[0m']},
 {name:'bracket-and-timestamp-continuations',messages:['root\n【警告】 2026-10-26 12:00:00,002 source - warning\n2026-10-26 12:00:00,003 [INFO] [OwnedApp] core - notice\n[WARN]: source - extra']},
 {name:'one-record-exceeds-display-line-limit',messages:[Array.from({length:605},()=> 'x').join('\n')]},
];
cases.push({name:'fixed-file-format-and-json-raw-bridge',messages:['first\nTraceback (owned):\n  detail'],raw_messages:['【INFO】2026-10-26 12:00:00,001 - plugins - first\nTraceback (owned):\n  detail']});
for(const c of cases){const chunks=(c.raw_messages||c.messages.map(message=>'INFO: 2026-10-26 12:00:00,001 plugins - '+message)).flatMap(message=>(message).split(/\r?\n/).map(line=>line.trim()).filter(Boolean));c.expected=run(chunks);}
const output={source:{commit:'2829e3b08214348f05ff2f5beae644882eccad58',path:'src/views/system/LoggingView.vue',sha256:crypto.createHash('sha256').update(original).digest('hex'),functions:names},kind:'actual fixed frontend functions; physical lines use fixed backend strip/nonempty behavior; synthetic owned log values',cases};
fs.writeFileSync(new URL('./source-oracle.json',import.meta.url),JSON.stringify(output,null,2)+'\n');console.log('fixed logging physical-line cases',cases.length);
