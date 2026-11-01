#!/usr/bin/env python3
"""Complete selected get_info enrichment, recording detail only, exact native zhconv."""
import __future__
import ast
import copy
from enum import Enum
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from types import SimpleNamespace
from typing import List, Optional, Union

ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE=ROOT/'audits/s11-provider-cn-source-preparation'
SOURCE=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
WHEEL_ROOT=ROOT/'source-runtime/zhconv-rs-0.4.2-official/static-wheel-members'
INIT=WHEEL_ROOT/'zhconv_rs/__init__.py'
EXTENSION=WHEEL_ROOT/'zhconv_rs/zhconv_rs.abi3.so'
PYC_PROBE=INIT.parent/'__pycache__'/('__init__.'+sys.implementation.cache_tag+'.pyc')
INIT_SHA='b0451ddab68bd5a5e2be6228538f409eaa28eb53adcc3bb57998ab35b0cb129b'
SO_SHA='c048aba5a41b4418754eeeba3457db5f5589522eab8010d646c156da66cf09e0'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv)==1

class CollectorBoundary(BaseException):
    pass

def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def verify_inputs(rows):
    for row in rows:
        raw=Path(row['path']).read_bytes()
        if len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:
            raise CollectorBoundary('input drift: '+row['path'])

def module_files():
    rows=[]
    for name,module in list(sys.modules.items()):
        path=getattr(module,'__file__',None)
        if path and Path(path).is_file():
            raw=Path(path).read_bytes()
            rows.append({'module':name,'path':str(Path(path).absolute()),'bytes':len(raw),'sha256':sha(raw)})
    return rows

def ordinary(value):
    if isinstance(value,Enum):
        return {'enum':type(value).__name__,'name':value.name,'value':value.value}
    if value is None or type(value) in (str,int,bool):
        return value
    if type(value) is list:
        return [ordinary(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k:ordinary(v) for k,v in value.items()}
    raise CollectorBoundary('unselected serialization: '+type(value).__name__)

manifest_raw=(HERE/'execution-inputs.json').read_bytes()
manifest=json.loads(manifest_raw)
verify_inputs(manifest['inputs'])
cases=json.loads((HERE/'owned-cases.json').read_bytes())
if len(cases)!=5 or len({x['id'] for x in cases})!=5:
    raise CollectorBoundary('fixed case count')
selected=json.loads((HERE/'source-nodes.json').read_bytes())
units=[]; observed=[]
# Only parse/compile fixed complete nodes here. Source definitions execute after guard.
for spec in selected['units']:
    path=SOURCE/spec['path']; raw=path.read_bytes()
    if sha(raw)!=spec['file_sha256']:
        raise CollectorBoundary('fixed source file drift')
    module=ast.parse(raw,filename=str(path))
    container=module if spec['class'] is None else next(n for n in module.body if isinstance(n,ast.ClassDef) and n.name==spec['class'])
    chosen=[]
    for expected in spec['nodes']:
        node=next(n for n in container.body if getattr(n,'name',None)==expected['name'])
        first=min([node.lineno]+[n.lineno for n in getattr(node,'decorator_list',[])])
        segment=b''.join(raw.splitlines(keepends=True)[first-1:node.end_lineno])
        got={'name':expected['name'],'first_line':first,'last_line':node.end_lineno,
             'ast_sha256':sha(ast.dump(node,include_attributes=True).encode()),'segment_sha256':sha(segment)}
        if got!=expected or any(isinstance(n,(ast.Import,ast.ImportFrom)) for n in ast.walk(node)):
            raise CollectorBoundary('complete original node drift/import')
        chosen.append(copy.deepcopy(node));observed.append({'path':spec['path'],'class':spec['class'],**got})
    body=chosen if spec['class'] is None else [ast.ClassDef(name=spec['class'],bases=[],keywords=[],body=chosen,decorator_list=[],type_params=[])]
    unit=ast.fix_missing_locations(ast.Module(body=body,type_ignores=[]))
    units.append(compile(unit,str(path),'exec',flags=__future__.annotations.compiler_flag,dont_inherit=True))
if len(observed)!=8:
    raise CollectorBoundary('source node count')
allowed_codes={id(x) for x in units}
forbidden=('app','requests','urllib3','socket','_socket','subprocess','ctypes','zhconv','zhconv_rs')
if any(n==p or n.startswith(p+'.') for n in sys.modules for p in forbidden):
    raise CollectorBoundary('forbidden/preloaded dependency')
pre_modules=module_files()
allowed_reads={r['path'] for r in manifest['inputs']}|{str(HERE/'execution-inputs.json')}
if any(r['path'] not in allowed_reads for r in pre_modules):
    raise CollectorBoundary('unbound stdlib input')
audit_counts={'imports':0,'source_unit_execs':0,'initializer_execs':0,'bytecode_refused':0}

def audit(event,args):
    if event=='import':
        if args[0] not in ('zhconv_rs','zhconv_rs.zhconv_rs'):
            raise CollectorBoundary('unselected post-setup import: '+str(args[0]))
        audit_counts['imports']+=1
    elif event=='open':
        path,mode,flags=args
        if not isinstance(path,(str,bytes,os.PathLike)):
            raise CollectorBoundary('unselected fd open')
        path=os.fsdecode(path)
        writes=flags&(os.O_WRONLY|os.O_RDWR|os.O_CREAT|os.O_TRUNC|os.O_APPEND)
        if path==str(PYC_PROBE) and not writes:
            audit_counts['bytecode_refused']+=1
            raise FileNotFoundError(2,'use bound initializer, not unbound bytecode',path)
        if writes or path not in allowed_reads:
            raise CollectorBoundary('unselected file access: '+path)
    elif event=='compile':
        source,filename=args
        if filename!=str(INIT):
            raise CollectorBoundary('unselected runtime compile')
        if type(source) is str:source=source.encode()
        if sha(source)!=INIT_SHA:
            raise CollectorBoundary('initializer compile drift')
    elif event=='exec':
        code=args[0]
        if id(code) in allowed_codes:
            audit_counts['source_unit_execs']+=1
        elif code.co_filename==str(INIT):
            audit_counts['initializer_execs']+=1
        else:
            raise CollectorBoundary('unselected exec')
    elif event=='os.listdir':
        if os.fsdecode(args[0]) not in (str(WHEEL_ROOT),str(WHEEL_ROOT/'zhconv_rs')):
            raise CollectorBoundary('unselected directory enumeration')
    elif (event.startswith(('socket.','subprocess.','ctypes.','urllib.','http.')) or
          event in ('os.system','os.exec','os.posix_spawn','os.fork','os.forkpty','os.remove','os.rmdir','os.rename','os.mkdir','os.chmod','os.chown','os.symlink','os.link','os.truncate','os.chdir','os.putenv','os.unsetenv')):
        raise CollectorBoundary('forbidden event: '+event)

sys.addaudithook(audit)
sys.path.insert(0,str(WHEEL_ROOT))
import zhconv_rs
native=sys.modules.get('zhconv_rs.zhconv_rs')
if (Path(zhconv_rs.__file__).absolute()!=INIT or native is None or Path(native.__file__).absolute()!=EXTENSION or sha(INIT.read_bytes())!=INIT_SHA or sha(EXTENSION.read_bytes())!=SO_SHA):
    raise CollectorBoundary('native package identity')
_zhconv=zhconv_rs.zhconv
for code in units:
    exec(code,globals())
original_adapter=convert
trace=[]

def traced_convert(text,target):
    if type(text) is not str or target!='zh-hans':
        raise CollectorBoundary('unselected native call')
    trace.append({'event':'original_convert_call','text':text,'target':target,'positional_args':2})
    result=original_adapter(text,target)
    if type(result) is not str:
        raise CollectorBoundary('unexpected converter result')
    trace.append({'event':'original_convert_return','value':result,'utf8_sha256':sha(result.encode())})
    return result

zhconv_convert=traced_convert
class RecordingLogger:
    def __init__(self,events):self.events=events
    def warn(self,message):self.events.append({'event':'log','level':'WARN','message':str(message)})
    def info(self,message):self.events.append({'event':'log','level':'INFO','message':str(message)})
    def error(self,message):self.events.append({'event':'log','level':'ERROR','message':str(message)})
    def debug(self,message):self.events.append({'event':'log','level':'DEBUG','message':str(message)})

results=[]
for case in cases:
    if case['mtype'] not in ('MOVIE','TV') or case['language']!='zh-CN' or case['tmdbid']!=900071:
        raise CollectorBoundary('unselected input profile')
    trace=[];logger=RecordingLogger(trace);detail=copy.deepcopy(case['detail'])
    receiver=object.__new__(TmdbApi)
    receiver.tmdb=SimpleNamespace(language=case['language'])
    reads=[]
    def detail_port(kind,tmdbid):
        if reads or kind!=case['mtype'] or tmdbid!=case['tmdbid']:
            raise CollectorBoundary('unselected detail request')
        reads.append(kind)
        trace.append({'event':'recording_detail_call','kind':kind,'tmdbid':tmdbid})
        trace.append({'event':'recording_detail_return','value':ordinary(detail)})
        return detail
    receiver._TmdbApi__get_movie_detail=lambda tmdbid:detail_port('MOVIE',tmdbid)
    receiver._TmdbApi__get_tv_detail=lambda tmdbid:detail_port('TV',tmdbid)
    before=ordinary(detail)
    try:
        value=receiver.get_info(MediaType[case['mtype']],case['tmdbid'])
    except Exception as exc:
        frames=[];tb=exc.__traceback__
        while tb is not None:
            frames.append({'file':tb.tb_frame.f_code.co_filename,'line':tb.tb_lineno,'function':tb.tb_frame.f_code.co_name});tb=tb.tb_next
        outcome={'kind':'uncaught_exception','class':type(exc).__name__,'message':str(exc),'frames':frames}
        same=None
    else:
        outcome={'kind':'return','python_type':type(value).__name__,'value':ordinary(value)};same=value is detail
    if len(reads)!=1:
        raise CollectorBoundary('unused recording detail')
    results.append({'id':case['id'],'input':case,'detail_before':before,'detail_after':ordinary(detail),'return_is_recording_detail':same,'outcome':outcome,'trace':trace})
post_modules=module_files()
allowed_module_names={r['module'] for r in pre_modules}|{'zhconv_rs','zhconv_rs.zhconv_rs'}
if any(r['module'] not in allowed_module_names for r in post_modules):
    raise CollectorBoundary('unselected loaded module')
verify_inputs(manifest['inputs'])
print(json.dumps({'status':'ORIGINAL_GET_INFO_CN_ENRICHMENT_OBSERVATIONS_COMPLETE','source_commit':selected['source_commit'],'manifest_sha256':sha(manifest_raw),'input_count':len(manifest['inputs']),'python':{'executable':sys.executable,'version':sys.version},'case_count':len(results),'cases':results,'source_nodes':observed,'module_files_before_native':pre_modules,'module_files_after_native':post_modules,'audit_counts':audit_counts,'input_drift':[],'boundaries':['Complete selected get_info/helpers and original convert wrapper; exact native zhconv two args.','Detail read methods only are recording ports; no real TMDB request/session/cache or complete MediaInfo.','No app imports/constructors; only fixed language state and ordinary JSON inputs.','Python auditing is not native syscall tracing.','Original uncaught error/partial mutation retained, no target-derived expected.']},ensure_ascii=False,indent=2,allow_nan=False))
