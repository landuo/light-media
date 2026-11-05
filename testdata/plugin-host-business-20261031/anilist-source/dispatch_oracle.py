#!/usr/bin/env python3
"""Execute fixed Chain ID selection / native exception handling, owned boundaries."""
import ast,copy,hashlib,json,pathlib,subprocess,types,typing,traceback
OUT=pathlib.Path(__file__).resolve().parent
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):return subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True)
chain=source('app/chain/__init__.py');media=source('app/utils/media.py')
media_nodes=[n for n in ast.parse(media).body if isinstance(n,ast.Assign)and any(isinstance(t,ast.Name)and t.id=='MEDIA_SOURCE_ALIASES'for t in n.targets)or isinstance(n,ast.FunctionDef)and n.name=='normalize_media_source']
env=dict(vars(typing),logger=types.SimpleNamespace(debug=lambda*x:None,error=lambda*x:None),traceback=traceback,EventType=types.SimpleNamespace(SystemError='system.error'),RateLimitExceededException=type('OwnedRateLimit',(Exception,),{}))
exec(compile(ast.fix_missing_locations(ast.Module(body=media_nodes,type_ignores=[])),COMMIT+':app/utils/media.py','exec'),env)
cls=next(n for n in ast.parse(chain).body if isinstance(n,ast.ClassDef)and n.name=='ChainBase')
nodes=[n for n in cls.body if isinstance(n,ast.FunctionDef)and n.name in {'_resolve_media_source_params','__is_valid_empty','__execute_system_modules','__handle_system_error'}]
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),COMMIT+':app/chain/__init__.py','exec'),env)
Chain=env['ChainBase'];cases=[]
for name,request in [('anilist_from_id',{'anilistid':151}),('tmdb_priority',{'tmdbid':999,'anilistid':151}),('explicit_anilist',{'source':'anilist','tmdbid':999,'anilistid':151}),('explicit_other_discards_anilist',{'source':'douban','anilistid':151}),('mediaid_priority',{'source':'anilist','mediaid':'152','anilistid':151}),('no_ids_default_left_to_module',{}),('source_alias',{'source':' TMDB ','tmdbid':999}),('unknown_source_kept',{'source':'Owned','anilistid':151})]:
 cases.append(dict(case=name,request=request,expected=Chain._resolve_media_source_params(**request)))
class OwnedFailure:
 def get_priority(self):return 4
 def get_name(self):return 'AniList'
 def recognize_media(self,**kwargs):raise AttributeError('owned absent characters mapping')
instance=Chain();errors=[];messages=[];instance.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[OwnedFailure()])
instance.messagehelper=types.SimpleNamespace(put=lambda **kwargs:messages.append(kwargs))
instance.eventmanager=types.SimpleNamespace(send_event=lambda event,data:errors.append(dict(event=event,data=data)))
result=instance._ChainBase__execute_system_modules('recognize_media',None,source='anilist',anilistid=151)
output=dict(source_commit=COMMIT,classification='fixed source AST: actual ChainBase selection and exception catcher, owned module fault boundary; no app imports, no production writes',source_hashes={p:hashlib.sha256(v.encode()).hexdigest()for p,v in [('app/chain/__init__.py',chain),('app/utils/media.py',media)]},cases=cases,module_error=dict(result=result,system_errors=errors,messages=messages),target='originalCatalogRecognitionCallKwargs / originalHostMediaRecognitionSource / originalCatalogRecognizeMethod')
(OUT/'dispatch-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(cases=len(cases),native_fault_result=result,system_errors=len(errors))))
