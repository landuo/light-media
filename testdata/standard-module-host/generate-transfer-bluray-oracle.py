"""Fixed TransHandler directory branch/recursion/result aggregation and disc naming.
Real fixed DTOs and methods run; storage/naming/transfer-command/event boundaries
are explicit owned adapters. Only disposable generated files are written.
"""
import ast,copy,json,os,tempfile,hashlib,subprocess
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
ns,one,run=env['ns'],env['one'],env['run'];Info,FileItem=ns['TransferInfo'],ns['FileItem']
for name in ['BaseEventData','ChainEventData','TransferInterceptEventData']:
 run([one('app/schemas/event.py',ast.ClassDef,name)],'<fixed:'+name+'>');ns[name].model_rebuild(_types_namespace=ns)
owner=one('app/modules/filemanager/transhandler.py',ast.ClassDef,'TransHandler')
names=['transfer_media','__normalize_disc_folder_name','__get_tv_bluray_dir_path','__build_preview_item','__update_result','__transfer_dir','__transfer_dir_files']
run([ast.ClassDef(name='TransHandler',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in names],decorator_list=[])],'<fixed:directory provider>')
Handler=ns['TransHandler'];Handler.get_naming_dict=lambda self,**kw:{}
Handler.get_rename_path=lambda self,**kw:kw['path']/'Owned (2024)'/'Owned (2024)'
ns['DirectoryHelper']=SimpleNamespace(get_media_root_path=lambda template,rename_path:rename_path.parent)
ns['settings']=SimpleNamespace(RENAME_FORMAT=lambda kind:'controlled naming boundary')
ns['MediaType']=SimpleNamespace(TV='tv')
ns['ChainEventType']=SimpleNamespace(TransferIntercept='transfer.intercept')
cases=[]
for mode in ['preview','success','partial_failure','intercept_cancel','empty','late_list_failure','renamed_members']:
 with tempfile.TemporaryDirectory(prefix='owned-bluray-source-')as td:
  root=Path(td);source=root/'source';target=root/'library';(source/'BDMV'/'STREAM').mkdir(parents=True);(source/'CERTIFICATE').mkdir()
  if mode!='empty':
   (source/'BDMV'/'STREAM'/'00001.m2ts').write_bytes(b'owned stream');(source/'BDMV'/'index.bdmv').write_bytes(b'owned index')
  trace=[];effects=[]
  def item(path):
   path=Path(path)
   if not path.exists():return None
   return FileItem(storage='local',type='dir'if path.is_dir()else'file',path=str(path)+('/'if path.is_dir()else''),name=path.name,basename=path.stem,extension=None if path.is_dir()else path.suffix[1:],size=None if path.is_dir()else path.stat().st_size)
  def listed(it):
   trace.append(['list',it.path])
   if mode=='late_list_failure' and Path(it.path).name=='CERTIFICATE':raise RuntimeError('owned late directory listing failure')
   # This is a controlled provider boundary. Freeze its iteration order so
   # late-list failure occurs after the earlier BDMV writes on every OS.
   children=sorted(Path(it.path).iterdir());out=[item(p)for directory in [True,False]for p in children if p.is_dir()==directory]
   if mode=='renamed_members':
    for child in out:child.name={'BDMV':'BluRay','STREAM':'Streams','00001.m2ts':'feature.m2ts','index.bdmv':'index-custom.bdmv'}.get(child.name,child.name)
   return out
  def got(path):trace.append(['get_item',str(path)]);return item(path)
  def folder(path):trace.append(['get_folder',str(path)]);Path(path).mkdir(parents=True,exist_ok=True);return item(path)
  def intercepted(kind,event):
   trace.append(['intercept',event.meta,event.options,str(event.target_path),Path(event.target_path).is_dir()])
   if mode=='intercept_cancel':event.cancel=True;event.reason='owned directory veto'
   return SimpleNamespace(event_data=event)
  def command(self,**kw):
   trace.append(['transfer_command',kw['fileitem'].path,str(kw['target_file'])])
   if mode=='partial_failure'and len(effects)==1:return None,'owned second-file failure'
   target_file=kw['target_file'];target_file.parent.mkdir(parents=True,exist_ok=True);os.link(kw['fileitem'].path,target_file);effects.append(str(target_file));return item(target_file),''
  Handler._TransHandler__transfer_command=command
  ns['eventmanager']=SimpleNamespace(send_event=intercepted)
  source_oper=SimpleNamespace(get_item=got,list=listed);target_oper=SimpleNamespace(get_folder=folder)
  result=Handler().transfer_media(fileitem=item(source),in_meta=SimpleNamespace(part=None),mediainfo=SimpleNamespace(type='movie'),target_storage='local',target_path=target,transfer_type='link',source_oper=source_oper,target_oper=target_oper,need_scrape=False,preview=mode=='preview')
  case={'mode':mode,'result':result.model_dump(mode='json'),'trace':trace,'effects':effects,'target_exists':(target/'Owned (2024)').exists(),'empty_directory_copied':(target/'Owned (2024)'/'CERTIFICATE').exists()}
  cases.append(json.loads(json.dumps(case,ensure_ascii=False).replace(td,'{root}')))
naming=[]
for part,name in [('Disc 02','Owned'),('','S01D03'),('DVD004','CD 09'),('','Owned disk_05'),('','盘片'),('',''),('','Disc ٠٧'),('','Disc\u200306')]:
 target=Handler._TransHandler__get_tv_bluray_dir_path(Path('/library/Show/Season 1/Show S01'),SimpleNamespace(name=name,path='/source/'+name),SimpleNamespace(part=part))
 naming.append({'part':part,'name':name,'effective_name':name or Path('/source/'+name).name,'output':target.name})
assert [c['result']['success']for c in cases]==[True,True,False,False,True,False,True],cases
source=Path('/Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py');raw=source.read_bytes();fixed=subprocess.check_output(['git','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/modules/filemanager/transhandler.py'],cwd=source.parents[3]);assert raw==fixed
Path(__file__).with_name('transfer-bluray-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(raw).hexdigest(),'source_methods':names,'kind':__doc__,'cases':cases,'naming':naming},ensure_ascii=False,indent=2)+'\n')
print(len(cases),'fixed directory branches;',len(naming),'fixed disc names')
