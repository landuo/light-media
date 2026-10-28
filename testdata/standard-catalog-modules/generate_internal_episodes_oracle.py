"""Pinned synchronous TmdbChain/ChainBase/provider stage for native internal season consumers.
Only registries, plugin callbacks and network response are controlled. Python is
an offline oracle and is not a runtime dependency of the Go application.
"""
import ast,copy,json,types
from pathlib import Path
p=Path(__file__).with_name('generate_episodes_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split('cases=[]')[0],str(p),'exec'),scope)
ns=scope['ns'];one=scope['one'];execute=scope['execute'];root=scope['root']
owner=one('app/chain/__init__.py',ast.ClassDef,'ChainBase');wanted={'__is_valid_empty','__handle_plugin_error','__handle_system_error','__handle_rate_limit_error','__execute_plugin_modules','__execute_system_modules','run_module'}
execute([ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in wanted],decorator_list=[])],'<fixed:sync engine>')
for path,source,target,bases,methods in [('app/chain/tmdb.py','TmdbChain','SyncEpisodes',[ast.Name(id='ChainBase',ctx=ast.Load())],['tmdb_episodes']),('app/modules/themoviedb/__init__.py','TheMovieDbModule','SyncNative',[],['tmdb_episodes'])]:
 owner=one(path,ast.ClassDef,source);execute([ast.ClassDef(name=target,bases=bases,keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in methods],decorator_list=[])],'<fixed:'+target+'>')
def clean(v):
 if isinstance(v,list):return [clean(x)for x in v]
 if isinstance(v,tuple):return {'__plugin_module_tuple':[clean(x)for x in v]}
 if hasattr(v,'model_dump'):return v.model_dump(mode='json')
 return v
native={'episode_number':1,'season_number':0,'name':'Owned native episode','id':1201}
plugin={'episode_number':2,'season_number':0,'name':'Owned sync plugin episode'}
cases=[]
for name,value,provider in [('none',None,[native]),('false',False,[native]),('zero',0,[native]),('dict',{},[native]),('list_empty',[],[native]),('list',[plugin],[native]),('tuple',(plugin,),[native]),('tuple_empty',(),[native]),('native_bad',None,[{'name':123}])]:
 encoded=copy.deepcopy(clean(value))
 calls=[];errors=[]
 def callback(*a,**kw):calls.append({'kind':'plugin','kwargs':kw});return value
 def obtain(**kw):calls.append({'kind':'provider','kwargs':kw});return {'episodes':copy.deepcopy(provider)}
 core=ns['SyncNative']();core.tmdb=types.SimpleNamespace(get_tv_season_detail=obtain);core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDb'
 chain=ns['SyncEpisodes']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'tmdb_episodes':callback}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None)
 result=chain.tmdb_episodes(tmdbid=120,season=0,episode_group=None)
 try:
  if not isinstance(result,(list,tuple)):raise TypeError('not an episode sequence')
  typed=[ns['TmdbEpisode'].model_validate(item).model_dump(mode='json')for item in result];valid=True
 except Exception:typed=None;valid=False
 cases.append({'name':name,'plugin':encoded,'native':provider,'calls':calls,'result':clean(result),'error_count':len(errors),'typed_projection':typed,'typed_sequence_valid':valid})
out={'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/tmdb.py:78','app/chain/__init__.py:470','app/modules/themoviedb/__init__.py:972','app/chain/transfer.py:1958','app/chain/subscribe.py:3795'],'kind':__doc__,'cases':cases}
Path(__file__).with_name('internal-episodes-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed sync actual-engine cases')
