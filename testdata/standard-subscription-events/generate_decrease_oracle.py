"""Actual fixed ordinary decrease/prepare methods with owned inventory results."""
import ast,copy,hashlib,json,pathlib,types
from enum import Enum
from typing import *
root=pathlib.Path('/Users/accidia/projects/MoviePilot');path=root/'app/chain/subscribe.py';raw=path.read_text();tree=ast.parse(raw);cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='SubscribeChain');media_path=root/'app/utils/media.py';media_raw=media_path.read_text();mt=ast.parse(media_raw)
ns=dict(copy=copy,Enum=Enum,Optional=Optional,Union=Union,List=List,Dict=Dict,Any=Any)
def execute(nodes,filename):
 for node in nodes:
  if isinstance(node,(ast.FunctionDef,ast.AsyncFunctionDef)):node.decorator_list=[]
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),filename,'exec'),ns)
types_path=root/'app/schemas/types.py';execute([n for n in ast.parse(types_path.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='MediaType'],'fixed/types.py')
execute([n for n in mt.body if (isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id in ['MEDIA_SOURCE_ALIASES','MEDIA_SOURCE_PREFIXES','MEDIA_SOURCE_ID_FIELDS'] for t in n.targets)) or (isinstance(n,ast.FunctionDef) and n.name in ['normalize_media_source','parse_media_key','resolve_media_identity','build_media_key'])],'fixed/media.py')
execute([n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name in ['_subscribe_media_key','_subscribe_media_keys']],'fixed/subscribe.py')
methods={n.name:n for n in cls.body if isinstance(n,ast.FunctionDef)};wanted=['__resolve_total_episode_decrease','__prepare_total_episode_change_fields'];execute([copy.deepcopy(methods[n]) for n in wanted],'fixed/subscribe.py')
base=dict(id=43,name='Owned decrease',type='电视剧',total_episode=8,start_episode=1,season=2,lack_episode=8,best_version=False,tmdbid=900043,doubanid=None,bangumiid=None,anilistid=None,media_source='themoviedb',media_id='900043',mediaid=None)
specs=[
 ('all_missing',list(range(1,9)),False,None),
 ('confirmed_highest7',[1,2,3,4,5,6,8],False,None),
 ('satisfied_keep_old',None,True,None),
 ('invalid_no_exists',None,False,'not_dict'),
 ('scope_mismatch',list(range(1,9)),False,'scope'),
 ('invalid_bool_episode',[True,2,3,4,5,6,7,8],False,None),
 ('inventory_error',None,False,'raise'),
 ('alternate_key',[1,2,3,4,5,6,8],False,'alternate'),
 ('lack_floor',list(range(1,9)),False,'low_lack'),
]
cases=[]
for label,missing,satisfied,mode in specs:
 fields=dict(base)
 if mode=='low_lack':fields['lack_episode']=1
 sub=types.SimpleNamespace(**fields);query=[];warnings=[]
 def resolve(**kwargs):
  query.append(dict(snapshot_total=kwargs['subscribe'].total_episode,snapshot_is_copy=kwargs['subscribe'] is not sub,key=kwargs['mediakey'],best_version_accept_downloaded=kwargs['best_version_accept_downloaded']))
  if mode=='raise':raise RuntimeError('owned inventory unavailable')
  if mode=='not_dict':return False,[]
  if satisfied:return True,None
  info=types.SimpleNamespace(season=2,start_episode=1,total_episode=7 if mode=='scope' else 8,episodes=missing)
  return False,{900043 if mode=='alternate' else 'tmdb:900043':{2:info}}
 ns['logger']=types.SimpleNamespace(warning=lambda message:warnings.append(message))
 owner=types.SimpleNamespace(resolve_subscribe_missing=resolve)
 setattr(owner,'__is_full_best_version_enabled',lambda subscribe:False)
 result=ns['__resolve_total_episode_decrease'](owner,subscribe=sub,candidate_total=5,meta={'owned':'meta'},mediainfo={'owned':'media'},mediakey='owned-other-key' if mode=='alternate' else None)
 prepared=ns['__prepare_total_episode_change_fields'](owner,subscribe=sub,total_episode=result,old_total_episode=8)
 cases.append(dict(name=label,subscription=fields,candidate_total=5,inventory=dict(missing=missing,satisfied=satisfied,mode=mode),result=result,prepared_fields=prepared,subscription_after=dict(vars(sub)),query=query,warning_count=len(warnings)))
assert [c['result'] for c in cases]==[5,7,8,5,5,5,5,7,5]
proof=[]
for name in ['__refresh_total_episode_before_completion','__finish_subscribe','check_and_handle_existing_media']:
 node=methods[name];calls=[n for n in ast.walk(node) if isinstance(n,ast.Call)];recognition=[n.lineno for n in calls if isinstance(n.func,ast.Attribute) and n.func.attr in ['recognize_media','async_recognize_media']]
 assert recognition==[]
 passed=[]
 for n in calls:
  if isinstance(n.func,ast.Attribute) and n.func.attr in ['__refresh_total_episode_before_completion','__finish_subscribe']:
   passed.append(dict(line=n.lineno,callee=n.func.attr,kwargs={k.arg:ast.unparse(k.value) for k in n.keywords}))
 proof.append(dict(function=name,line=node.lineno,parameters=[a.arg for a in node.args.args],recognition_calls=recognition,downstream_same_meta_media=passed))
finish_calls=[]
for n in ast.walk(tree):
 if isinstance(n,ast.Call) and isinstance(n.func,ast.Attribute) and n.func.attr=='__finish_subscribe':finish_calls.append(dict(line=n.lineno,kwargs={k.arg:ast.unparse(k.value) for k in n.keywords}))
assert len(finish_calls)==2 and all(c['kwargs']['meta']=='meta' and c['kwargs']['mediainfo']=='mediainfo' for c in finish_calls)
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind='actual fixed ordinary decrease/prepare AST; owned missing-inventory responses and full-best-version-disabled boundary, no old application/service',source_sha256={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [path,media_path,types_path]},cases=cases,flow_proof=proof,finish_calls=finish_calls,scope='ordinary subscriptions only; best-version prepare branch not executed or accepted here')
pathlib.Path(__file__).with_name('decrease-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print('actual decrease/prepared cases',len(cases),'AST no second recognition proof',len(proof))
