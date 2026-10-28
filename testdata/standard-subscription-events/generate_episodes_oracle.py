"""Actual fixed ordinary total-episode chain consumer and Pydantic model."""
import ast,hashlib,json
from pathlib import Path
from types import SimpleNamespace
from typing import Any,Optional
from pydantic import BaseModel,Field
root=Path('/Users/accidia/projects/MoviePilot')
schema=root/'app/schemas/event.py';chain=root/'app/chain/subscribe.py'
names=['BaseEventData','ChainEventData','SubscribeEpisodesRefreshEventData']
nodes=[x for x in ast.parse(schema.read_text()).body if isinstance(x,ast.ClassDef) and x.name in names]
ns=dict(BaseModel=BaseModel,Field=Field,Any=Any,Optional=Optional)
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'fixed-episode-schema','exec'),ns)
method=next(x for x in ast.walk(ast.parse(chain.read_text())) if isinstance(x,ast.FunctionDef) and x.name=='__apply_episodes_refresh')
method.name='apply_episodes';method.decorator_list=[]
for arg in method.args.args+method.args.kwonlyargs:arg.annotation=None
method.returns=None
ns.update(ChainEventType=SimpleNamespace(SubscribeEpisodesRefresh='subscribe.episodes.refresh'))
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-episodes-consumer','exec'),ns)
cases=[]
for name,updated,total in [('unchanged',False,8),('raise',True,8),('clamp-low',True,2),('false-total',True,0),('negative',True,-1)]:
    inputs=[]
    def send(kind,data):
        inputs.append(data.model_dump())
        data.updated=updated;data.total_episode=total
        return SimpleNamespace(event_data=data)
    ns['eventmanager']=SimpleNamespace(send_event=send)
    result=ns['apply_episodes'](5,4,mediainfo={'source':'themoviedb','tmdb_id':206484,'seasons':{'4':[1,2,3,4,5]}},tmdbid=206484,media_source='themoviedb',media_id='206484',scene='create')
    cases.append({'name':name,'current':5,'updated':updated,'total':total,'input':inputs[0],'result':result})
print(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed Pydantic model and __apply_episodes_refresh method; captured synchronous chain mutation only','source_sha256':{str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [schema,chain]},'cases':cases},ensure_ascii=False,indent=2))
