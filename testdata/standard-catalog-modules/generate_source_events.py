"""Execute fixed source endpoints and their real Pydantic source DTO classes.
Only EventManager delivery is controlled; no target code creates expectations.
"""
import ast,copy,hashlib,json
from pathlib import Path
from types import SimpleNamespace
from typing import Optional,List,Dict,Any
from pydantic import BaseModel,Field
root=Path('/Users/accidia/projects/MoviePilot');ns=dict(BaseModel=BaseModel,Field=Field,Optional=Optional,List=List,Dict=Dict,Any=Any);hashes={}
p=root/'app/schemas/event.py';text=p.read_text();hashes[str(p.relative_to(root))]=hashlib.sha256(text.encode()).hexdigest()
names=['BaseEventData','ChainEventData','DiscoverMediaSource','DiscoverSourceEventData','RecommendMediaSource','RecommendSourceEventData']
tree=ast.parse(text);nodes=[n for n in tree.body if isinstance(n,ast.ClassDef) and n.name in names];exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<fixed:event schemas>','exec'),ns)
class Events:
 def send_event(self,kind,data):
  self.input=data.model_dump(mode='json');self.kind=kind
  if self.mutation is not None:data.extra_sources=copy.deepcopy(self.mutation)
  return SimpleNamespace(event_data=data) if self.deliver else None
events=Events();ns.update(eventmanager=events,schemas=SimpleNamespace(TokenPayload=Any),ChainEventType=SimpleNamespace(RecommendSource='recommend.source',DiscoverSource='discover.source'))
ns['Depends']=lambda fn:None;ns['verify_token']=lambda:None
cases=[]
for kind in ['recommend','discover']:
 p=root/f'app/api/endpoints/{kind}.py';text=p.read_text();hashes[str(p.relative_to(root))]=hashlib.sha256(text.encode()).hexdigest();f=copy.deepcopy(next(n for n in ast.parse(text).body if isinstance(n,ast.FunctionDef) and n.name=='source'));f.decorator_list=[]
 ns.update(RecommendSourceEventData=ns['RecommendSourceEventData'],DiscoverSourceEventData=ns['DiscoverSourceEventData']);exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),'<fixed:'+kind+'>','exec'),ns)
 sample=({'name':'Owned ordinary recommendations','api_path':'plugin/OwnedCatalogFixture/feed?list=ordinary','type':'电影'} if kind=='recommend' else {'name':'Owned ordinary discover','mediaid_prefix':'owned','api_path':'plugin/OwnedCatalogFixture/feed','filter_params':{'type':'电影','genre':'all','region':None},'filter_ui':[{'component':'VTextField','props':{'model':'genre','label':'Owned genre'}}],'depends':{'region':['genre']}})
 for label,deliver,mutation in [('no-subscriber',False,None),('unchanged',True,None),('appended',True,[sample]),('empty',True,[]),('falsey',True,False),('invalid-truthy',True,True),('default-fields',True,[{k:sample[k] for k in (['name','api_path','type'] if kind=='recommend' else ['name','mediaid_prefix','api_path'])}])]:
  events.deliver=deliver;events.mutation=mutation
  returned=ns['source'](None);model=ns['RecommendMediaSource' if kind=='recommend' else 'DiscoverMediaSource']
  try:output=[model.model_validate(x).model_dump(mode='json') for x in returned];state='valid'
  except Exception:output=None;state='response-invalid'
  cases.append({'kind':kind,'name':label,'deliver':deliver,'mutation':mutation,'event_type':events.kind,'event_input':events.input,'returned':returned,'state':state,'output':output})
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed source endpoints plus actual Pydantic source models; EventManager delivery controlled','sources':hashes,'cases':cases}
Path(__file__).with_name('source-events-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed ordinary source endpoint/model scenarios')
