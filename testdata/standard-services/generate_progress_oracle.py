"""Actual fixed ScheduleProgress model with the host's Chinese identity locale."""
import ast,hashlib,json
from pathlib import Path
from typing import Optional
from types import SimpleNamespace
from pydantic import BaseModel,Field,model_validator
path=Path('/Users/accidia/projects/MoviePilot/app/schemas/dashboard.py');tree=ast.parse(path.read_text());node=next(x for x in tree.body if isinstance(x,ast.ClassDef) and x.name=='ScheduleProgress')
ns={'Optional':Optional,'BaseModel':BaseModel,'Field':Field,'model_validator':model_validator,'LocaleHelper':SimpleNamespace(get_current_locale=lambda:'zh-CN',translate_text=lambda text,locale=None:text)}
exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'fixed-service-progress-model','exec'),ns)
base={'id':'owned','name':'Owned','provider':'Provider','enable':True,'value':37,'text':'Owned progress','status':'running','success':None,'started_at':None,'finished_at':None,'error':None,'data':{}}
cases=[]
for name,patch in [('normal',{}),('success-string',{'success':'false'}),('success-numeric',{'success':1}),('name-invalid',{'name':1}),('success-invalid',{'success':[]})]:
 data={**base,**patch};output=None;error=None
 try:output=ns['ScheduleProgress'].model_validate(data).model_dump()
 except Exception as e:error=type(e).__name__
 cases.append({'name':name,'input':data,'output':output,'error':error})
print(json.dumps({'source':'app/schemas/dashboard.py:85','source_sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'kind':'actual fixed Pydantic model; Chinese identity locale follows current Go host, no global translation system added','cases':cases},indent=2))
