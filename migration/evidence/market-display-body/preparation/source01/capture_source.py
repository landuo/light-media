#!/usr/bin/env python3
"""Six bounded calls to the intact original display-index parser, no HTTP/cache."""
import __future__
import ast
import copy
import hashlib
import json
import os
import sys
from pathlib import Path

HERE=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/target-drafts/market-display-body01/source01')
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv)==1

class CollectorBoundary(BaseException): pass

def sha(b): return hashlib.sha256(b).hexdigest()
def forbidden_imports():
    denied=('app','requests','urllib3','socket','_socket','ssl','_ssl','http','urllib.request','subprocess','multiprocessing','ctypes','tzlocal')
    bad=[n for n in sys.modules if any(n==p or n.startswith(p+'.') for p in denied)]
    if bad: raise CollectorBoundary('forbidden imports: '+','.join(bad))

def check_inputs(manifest):
    for item in manifest['inputs']:
        raw=Path(item['path']).read_bytes()
        if len(raw)!=item['bytes'] or sha(raw)!=item['sha256']:
            raise CollectorBoundary('input drift: '+item['path'])
    for path in manifest['absent_cache_paths']:
        if Path(path).exists(): raise CollectorBoundary('absent cache appeared: '+path)

def observed_modules(manifest):
    rows={r['path']:r for r in manifest['inputs']};observed={}
    forbidden_imports()
    for name,module in tuple(sys.modules.items()):
        path=getattr(module,'__file__',None)
        if not path or not Path(path).is_file(): continue
        path=str(Path(path).resolve())
        if path not in rows: raise CollectorBoundary('unbound module: '+path)
        raw=Path(path).read_bytes()
        if sha(raw)!=rows[path]['sha256']: raise CollectorBoundary('module drift: '+path)
        observed[name]={'path':path,'sha256':sha(raw)}
    return observed

def install_audit(manifest):
    reads={str(Path(r['path']).resolve()) for r in manifest['inputs']}
    reads.add(str(HERE/'input-bindings.json'))
    rejected=[]
    def reject(event):
        rejected.append(event);raise CollectorBoundary('forbidden audit event: '+event)
    def audit(event,args):
        if event=='open':
            path,mode,flags=args
            if not isinstance(path,(str,bytes,os.PathLike)): reject('open-fd')
            if flags & (os.O_WRONLY|os.O_RDWR|os.O_CREAT|os.O_TRUNC|os.O_APPEND): reject('open-write')
            if str(Path(path).resolve()) not in reads: reject('open-unbound')
        if event.startswith(('socket.','subprocess.','ctypes.')) or event in {'os.system','os.fork','os.posix_spawn','os.exec','os.remove','os.rename','os.rmdir','os.mkdir','os.chmod','os.chown','os.link','os.symlink','os.truncate','os.putenv','os.unsetenv'}:
            reject(event)
    sys.addaudithook(audit)
    return rejected

class RecordingLogger:
    def __init__(self): self.rows=[]
    def warn(self,message):
        raw=message.encode('utf-8')
        # Preserve the actual source log's exact identity without duplicating
        # the deliberately large owned payload in a warning body.
        self.rows.append({'level':'WARN','utf8_bytes':len(raw),'sha256':sha(raw)})

def make_source(bound,logger):
    raw=Path(bound['path']).read_bytes()
    if sha(raw)!=bound['sha256']: raise CollectorBoundary('source drift')
    original=next(n for n in ast.parse(raw,filename=bound['path']).body if isinstance(n,ast.ClassDef) and n.name=='PluginHelper')
    node=next(n for n in original.body if isinstance(n,ast.FunctionDef) and n.name=='__parse_plugin_index_response')
    actual=sha(ast.dump(node,include_attributes=True).encode())
    if actual!=bound['node_ast_sha256'] or any(isinstance(n,(ast.Import,ast.ImportFrom)) for n in ast.walk(node)):
        raise CollectorBoundary('selected original AST drift/import')
    # Scaffolding retains the original name for private-name binding. No
    # original class metaclass/constructor/package initialization is executed.
    scaffold=ast.ClassDef(name='PluginHelper',bases=[],keywords=[],body=[copy.deepcopy(node)],decorator_list=[],type_params=[])
    future=ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)
    unit=ast.fix_missing_locations(ast.Module(body=[future,scaffold],type_ignores=[]))
    namespace={'__name__':'__owned_original_package_parser__','json':json,'logger':logger}
    exec(compile(unit,bound['path'],'exec'),namespace)
    return namespace['PluginHelper']._PluginHelper__parse_plugin_index_response

def materialize(case):
    if set(case)!={'id','prefix','repeat','count','suffix'} or case['repeat'] not in ('x',' ') or case['count'] not in (0,2<<20):
        raise CollectorBoundary('outside fixed finite input recipe')
    if len(case['prefix'])>256 or len(case['suffix'])>32:
        raise CollectorBoundary('unbounded fixed recipe component')
    return case['prefix']+case['repeat']*case['count']+case['suffix']

def main():
    forbidden_imports()
    manifest_raw=(HERE/'input-bindings.json').read_bytes();manifest=json.loads(manifest_raw)
    check_inputs(manifest)
    if str(Path(sys.executable).resolve())!=manifest['python_resolved']:
        raise CollectorBoundary('unexpected Python executable')
    before=observed_modules(manifest)
    bound=json.loads((HERE/'source-node.json').read_text())
    cases=json.loads((HERE/'owned-cases.json').read_text())['cases']
    if len(cases)!=6 or len({c['id'] for c in cases})!=6: raise CollectorBoundary('case count drift')
    rejected=install_audit(manifest)
    count=0
    def budget(frame,event,arg):
        nonlocal count
        if event=='line' and frame.f_code.co_filename==bound['path']:
            count+=1
            if count>1000: raise CollectorBoundary('source collector line budget exhausted')
        return budget
    outputs=[]
    for case in cases:
        text=materialize(case);input_raw=text.encode('utf-8');input_sha=sha(input_raw)
        logger=RecordingLogger();original=make_source(bound,logger)
        sys.settrace(budget)
        try:
            value=original(text)
        except Exception as error:
            outcome={'kind':'uncaught_exception','class':type(error).__name__,'message':str(error)}
        else:
            outcome={'kind':'return','python_type':type(value).__name__,'value':value}
        finally:
            sys.settrace(None)
        outputs.append({'id':case['id'],'input_recipe':case,'input_utf8_bytes':len(input_raw),'input_sha256':input_sha,
                        'after_input_sha256':sha(text.encode('utf-8')),'outcome':outcome,'actual_logs':logger.rows})
    after=observed_modules(manifest);check_inputs(manifest)
    if (HERE/'input-bindings.json').read_bytes()!=manifest_raw: raise CollectorBoundary('manifest changed')
    print(json.dumps({'status':'ORIGINAL_PACKAGE_PARSER_SIX_FINITE_CALLS_COMPLETE','source_commit':manifest['source_commit'],
                      'python':sys.version,'executable':sys.executable,'manifest_sha256':sha(manifest_raw),
                      'case_count':len(outputs),'source_calls':len(outputs),'source_line_events':count,'source_node':bound,
                      'cases':outputs,'modules_before':before,'modules_after':after,'input_drift':[],
                      'audit_rejects':rejected,'forbidden_imports_absent':True,
                      'scope':'Complete original parser on six finite owned text inputs; no original HTTP, cache decorator, Manager, source module startup or target execution.'},ensure_ascii=False,indent=2,allow_nan=False))

if __name__=='__main__': main()
