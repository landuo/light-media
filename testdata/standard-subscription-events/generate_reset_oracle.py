"""Pinned reset endpoint body and actual modified-event model, owned row IO."""
import ast,asyncio,copy,hashlib,json
from pathlib import Path
from types import SimpleNamespace
from typing import Any,Dict,List
from pydantic import BaseModel,Field,model_validator
root=Path('/Users/accidia/projects/MoviePilot');api=root/'app/api/endpoints/subscribe.py';schema=root/'app/schemas/event.py'
ns=dict(BaseModel=BaseModel,Field=Field,model_validator=model_validator,Any=Any,Dict=Dict,List=List)
nodes=[x for x in ast.parse(schema.read_text()).body if isinstance(x,ast.ClassDef) and x.name in ['BaseEventData','SubscribeModifiedEventData']]
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'fixed-reset-schema','exec'),ns)
method=next(x for x in ast.walk(ast.parse(api.read_text())) if isinstance(x,ast.AsyncFunctionDef) and x.name=='reset_subscribes')
method.decorator_list=[];method.returns=None
for arg in method.args.args:arg.annotation=None
method.args.defaults=[ast.Constant(None) for x in method.args.defaults]
before={'id':7,'total_episode':5,'lack_episode':2,'note':[1,2,3],'current_priority':70,'episode_priority':{'1':70},'manual_total_episode':1,'state':'S','keyword':'retained'}
class Row:
    def __init__(self):self.data=copy.deepcopy(before);self.total_episode=5
    def to_dict(self):return copy.deepcopy(self.data)
    async def async_update(self,db,data):self.data.update(data)
row=Row();events=[]
async def accessible(*args):return row
async def get(*args):return row
async def send(kind,data):events.append({'type':kind,'data':data})
ns.update(get_accessible_subscribe=accessible,Subscribe=SimpleNamespace(async_get=get),eventmanager=SimpleNamespace(async_send_event=send),EventType=SimpleNamespace(SubscribeModified='subscribe.modified'),schemas=SimpleNamespace(Response=lambda **kwargs:kwargs))
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-reset-endpoint','exec'),ns)
response=asyncio.run(ns['reset_subscribes'](7,None,None))
print(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed reset endpoint and SubscribeModifiedEventData; owned row/database dependency, not actual original service','source_sha256':{str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [api,schema]},'before':before,'after':row.to_dict(),'events':events,'response':response},ensure_ascii=False,indent=2))
