"""Fixed sibling collector, single-file planner and ordinary storage dispatcher.
Only storage providers' FileItem results, extension lists, metadata recognition
and download-history lookups are controlled. No filesystem publication runs.
"""
import ast, copy, json, subprocess
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-browser-storage-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('base={')[0],str(p),'exec'),env)
ns,run=env['ns'],env['run'];FileItem=ns['FileItem'];fixed='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):
 return ast.parse(subprocess.check_output(['git','-C',str(env['root']),'show',fixed+':'+path],text=True))
def owner(path,name):return copy.deepcopy(next(n for n in source(path).body if isinstance(n,ast.ClassDef)and n.name==name))
storage=owner('app/chain/storage.py','StorageChain')
run([ast.ClassDef(name='StorageChain',bases=[ast.Name(id='MediaChain',ctx=ast.Load())],keywords=[],body=[n for n in storage.body if isinstance(n,ast.FunctionDef)and n.name in ['list_files','get_parent_item']],decorator_list=[])],'<fixed:storage wrappers>')
Storage=ns['StorageChain'];transfer=owner('app/chain/transfer.py','TransferChain')
do=next(n for n in transfer.body if isinstance(n,ast.FunctionDef)and n.name=='do_transfer')
names=['__is_subtitle_file','__is_audio_file','__is_media_file','__is_allow_filesize','__get_file_key','__get_file_stem','__get_subtitle_media_stem','__get_extra_media_stem','__get_related_main_file_key','__normalize_dir_path','__get_dir_key','__get_file_parent_key']
helpers=[copy.deepcopy(n)for n in ast.walk(do)if isinstance(n,ast.FunctionDef)and n.name in ['_is_allowed_transfer_item','_append_item','_build_directory_index','_get_single_file_sibling_items','_plan_file_items']]
body=ast.parse('matched_episode_format_template=False').body+helpers+ast.parse('return _plan_file_items([(fileitem,False)])').body
wrapper=ast.FunctionDef(name='plan',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self'),ast.arg(arg='fileitem')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=body,decorator_list=[])
run([ast.ClassDef(name='TransferChain',bases=[],keywords=[],body=[n for n in transfer.body if isinstance(n,ast.FunctionDef)and n.name in names]+[wrapper],decorator_list=[])],'<fixed:sibling collector and complete single-file planner>')
tags=next(n for n in source('app/chain/transfer.py').body if isinstance(n,ast.Assign)and any(isinstance(t,ast.Name)and t.id=='SUBTITLE_STEM_TAGS'for t in n.targets));run([tags],'<fixed:subtitle tags>')
ns.update(continue_callback=None,has_episode_format_template=False,formaterHandler=None,min_filesize=0,transfer_exclude_words=[],sync_extra_files=True,download_hash=None,DownloadHistoryOper=lambda:None,deepcopy=copy.deepcopy,_build_file_meta=lambda *a,**kw:SimpleNamespace(name='Owned'),_build_path_meta=lambda *a,**kw:None)
chain=ns['TransferChain']();chain._media_exts=['.mkv','.mp4'];chain._subtitle_exts=['.srt'];chain._audio_exts=['.flac'];chain._is_blocked_by_exclude_words=lambda *a:False;chain._resolve_download_history=lambda **kw:None;chain._get_subscribe_custom_words=lambda value:None
root='{root}/source';main={'storage':'local','type':'file','path':root+'/Owned.2024.mkv','name':'Owned.2024.mkv','extension':'mkv','size':26};parent={'storage':'local','type':'dir','path':root+'/','name':'source'}
def item(name,**kw):return dict(storage='local',type='file',path=root+'/'+name,name=name,extension=Path(name).suffix[1:],size=24,**kw)
sub=item('Owned.2024.zh.srt');audio=item('Owned.2024.flac');native=[main,sub,audio]
cases=[('native',None,None,native),('parent_false',False,None,native),('list_false',None,False,native),('list_empty',None,[],native),('tuple',None,tuple([audio,sub]),native),('duplicate_list',None,[sub],native),('duplicate_tuple',None,tuple([sub,sub,audio]),native),('tuple_falsey',None,tuple([None,False,audio]),native),('scalar_list',None,True,native),('dict_list',None,{'x':1},native),('scalar_parent',True,tuple([audio]),native),('ambiguous_sibling_main',None,None,[main,item('Owned.2024.mp4'),sub,audio]),('null_extension',None,tuple([dict(sub,extension=None),audio]),native),('metadata_name',None,tuple([dict(sub,path=root+'/unrelated.bin',extension='srt',name='Owned.2024.zh.srt')]),native),('hidden',None,tuple([dict(sub,path=root+'/.hidden/Owned.2024.zh.srt'),audio]),native),('large_extra',None,tuple([dict(audio,size=70*1024*1024)]),native),('forty_extras',None,tuple([dict(sub,path=root+f'/extra-{i}.srt')for i in range(40)]),native),('large_native_directory',None,None,[item(f'Other{i}.mkv')for i in range(2001)]+native)]
rows=[]
for name,parent_value,list_value,local in cases:
 trace=[]
 def models(v):
  if isinstance(v,list):return [models(x)for x in v]
  if isinstance(v,tuple):return tuple(models(x)for x in v)
  if isinstance(v,dict)and 'path'in v:return FileItem.model_validate(v)
  return v
 def get_parent_item(fileitem):trace.append({'method':'get_parent_item','fileitem':fileitem.model_dump(mode='json')});return models(copy.deepcopy(parent_value))
 def list_files(fileitem,recursion=False):trace.append({'method':'list_files','fileitem':fileitem.model_dump(mode='json')if hasattr(fileitem,'model_dump')else fileitem,'recursion':recursion});return models(copy.deepcopy(list_value))
 class Core:
  def get_priority(self):return 0
  def get_name(self):return '文件整理'
  def get_parent_item(self,fileitem):trace.append({'method':'native_parent'});return models(parent)
  def list_files(self,fileitem,recursion=False):trace.append({'method':'native_list'});return models(copy.deepcopy(local))
 storage=Storage();storage.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'get_parent_item':get_parent_item,'list_files':list_files}});storage.modulemanager=SimpleNamespace(get_running_modules=lambda method:[Core()]);storage.messagehelper=SimpleNamespace(put=lambda **kw:None);storage.eventmanager=SimpleNamespace(send_event=lambda *a:None);ns['StorageChain']=lambda:storage
 error=None
 try:planned,inherited=chain.plan(FileItem.model_validate(main));out=[x.model_dump(mode='json')for x,_ in planned]
 except Exception as e:out=None;error=type(e).__name__
 def encode(v):return {'__plugin_module_tuple':list(v)}if isinstance(v,tuple)else v
 rows.append(dict(name=name,parent=parent_value,plugin=encode(list_value),native=local,output=out,exception=error,trace=trace))
 assert (error is not None)==(name in ['scalar_list','dict_list']),(name,error)
Path(__file__).with_name('transfer-collection-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source=['app/chain/transfer.py:2733-2814','app/chain/transfer.py:3073-3370','app/chain/storage.py:get_parent_item/list_files','app/chain/__init__.py:run_module'],kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed sibling storage/complete single-file planner cases')
