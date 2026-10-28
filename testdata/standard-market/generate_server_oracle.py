"""Pinned center client functions and rating models; owned responses only."""
import ast, asyncio, hashlib, json, subprocess, sys
from pathlib import Path
from types import SimpleNamespace as NS
from typing import Optional
from urllib.parse import parse_qs,quote,urlsplit
import pydantic
from pydantic import BaseModel, Field, ValidationError

commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
paths=['app/helper/server.py','app/schemas/plugin.py']
sources={p:subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+p]) for p in paths}
models=[node for node in ast.parse(sources[paths[1]]).body if isinstance(node,ast.ClassDef) and node.name in ['PluginRating','PluginRatingRequest']]
model_ns=dict(BaseModel=BaseModel,Field=Field,Optional=Optional)
exec(compile(ast.fix_missing_locations(ast.Module(body=models,type_ignores=[])),'fixed/schemas/plugin.py','exec'),model_ns)
schema_cases=[]
for payload in [dict(rating=x) for x in [0,.1,1,4.5,5,5.1,4.55,True,False,'4.5',' 4.5 ','4_._5','0x1','1e0','1_0e-1','NaN','inf',None,[],{},'_4.5','4.5_','4__5e-1','+4.5','４.５','4\u00a0','.5','1.',0.10000000001,0.1000000001,0.100000001,0.100000000001]]+[{},dict(rating=4.5,ignored='source permits extras')]:
    try: value=model_ns['PluginRatingRequest'].model_validate(payload).model_dump();success=True
    except ValidationError: value=None;success=False
    schema_cases.append(dict(kind='request',input=payload,success=success,value=value))
for payload in [dict(plugin_id='Owned'),dict(plugin_id='Owned',average_rating='4.2',rating_count='3',user_rating=True),dict(plugin_id='Owned',average_rating=-1,rating_count=-3,user_rating=6.5),dict(plugin_id='Owned',rating_count=2**80),dict(plugin_id='Owned',rating_count=3.0),dict(plugin_id='Owned',rating_count='3.0'),dict(plugin_id='Owned',rating_count='3e0'),dict(plugin_id='Owned',average_rating=None),dict(plugin_id='Owned',rating_count=3.5),dict(plugin_id=1),{}]:
    try: value=model_ns['PluginRating'].model_validate(payload).model_dump();success=True
    except ValidationError: value=None;success=False
    schema_cases.append(dict(kind='response',input=payload,success=success,value=value))

names=['async_get_plugin_statistic','async_get_plugin_ratings','async_get_plugin_rating','async_submit_plugin_rating','async_install_plugin_reg','sanitize_plugin_repo_url','_parse_local_repo_plugin_id','_parse_local_repo_package_version','_make_local_repo_url']
nodes=[node for node in ast.walk(ast.parse(sources[paths[0]])) if isinstance(node,(ast.FunctionDef,ast.AsyncFunctionDef)) and node.name in names]
assert len(nodes)==9
owner=ast.ClassDef(name='OwnedFixedHelper',bases=[],keywords=[],body=nodes,decorator_list=[])
ns=dict(settings=NS(PLUGIN_STATISTIC_SHARE=True),logger=NS(debug=lambda *args:None),parse_qs=parse_qs,quote=quote,urlsplit=urlsplit)
future=ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)
exec(compile(ast.fix_missing_locations(ast.Module(body=[future,owner],type_ignores=[])),'fixed/helper/server.py','exec'),ns)
helper=ns['OwnedFixedHelper'];helper._LOCAL_REPO_PREFIX='local://';cases=[]
for mode in ['none','http_failure','transport_exception','valid','empty_object','json_exception','error_object']:
    async def boundary(*args):
        if mode=='none': return None
        if mode=='transport_exception': raise RuntimeError('owned transport unavailable')
        def body():
            if mode=='json_exception': raise ValueError('owned invalid JSON')
            return {'success':False,'message':'owned center said no'} if mode=='error_object' else {'owned':'actual center payload'} if mode=='valid' else {}
        return NS(status_code=503 if mode=='http_failure' else 200,json=body)
    for method in ['async_plugin_statistic','async_plugin_ratings','async_plugin_rating','async_rate_plugin','async_plugin_install']: setattr(helper,method,staticmethod(boundary))
    for name,args in [('async_get_plugin_statistic',()),('async_get_plugin_ratings',(['Owned'],)),('async_get_plugin_rating',('Owned',)),('async_submit_plugin_rating',('Owned',4.5)),('async_install_plugin_reg',('Owned',None))]:
        try: value=asyncio.run(getattr(helper,name)(*args));error=None
        except Exception as exc: value=None;error=type(exc).__name__
        cases.append(dict(mode=mode,method=name,value=value,error=error))
assert next(c for c in cases if c['mode']=='none' and c['method']=='async_get_plugin_rating')['value']==dict(plugin_id='Owned',average_rating=0.0,rating_count=0,user_rating=None)
assert next(c for c in cases if c['mode']=='error_object' and c['method']=='async_install_plugin_reg')['value'] is True
Path(__file__).with_name('server-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256={path:hashlib.sha256(raw).hexdigest() for path,raw in sources.items()},pydantic_version=pydantic.__version__,kind='actual fixed async client functions and Pydantic models; owned transport only, not official center acceptance',client_cases=cases,schema_cases=schema_cases),ensure_ascii=False,indent=2)+'\n')
