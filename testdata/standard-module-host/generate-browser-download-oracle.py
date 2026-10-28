"""Fixed StorageChain download wrapper, dispatcher, FileManager and LocalStorage,
plus download/image endpoint AST. Owned temporary bytes and plugin values.
Starlette response adapter is a development boundary with recorded version.
"""
import ast,asyncio,copy,hashlib,json,subprocess,tempfile
from pathlib import Path
from types import SimpleNamespace as NS
from pydantic import model_validator
from starlette.responses import FileResponse,Response
from starlette.exceptions import HTTPException
import starlette
p=Path(__file__).with_name('generate-browser-storage-oracle.py');env={'__file__':str(p)};exec(compile(p.read_text().split('base={')[0],str(p),'exec'),env)
ns,root,one,run,FileItem=env['ns'],env['root'],env['one'],env['run'],env['FileItem'];ns.update(model_validator=model_validator,LocaleHelper=NS(translate_text=lambda t,**kw:t,get_current_locale=lambda:None),FileResponse=FileResponse,Response=Response,HTTPException=HTTPException)
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';paths=['app/chain/__init__.py','app/chain/storage.py','app/modules/filemanager/__init__.py','app/modules/filemanager/storages/local.py','app/api/endpoints/storage.py','app/schemas/file.py','app/schemas/response.py'];hashes={}
for path in paths:
 raw=subprocess.check_output(['git','show',fixed+':'+path],cwd=root);assert raw==(root/path).read_bytes();hashes[path]=hashlib.sha256(raw).hexdigest()
run([one('app/schemas/response.py',ast.ClassDef,'Response')],'<fixed Response>');ns['schemas'].Response=ns['Response'];ns['Response'].model_rebuild(_types_namespace=ns);ns['Response']=Response
for path,name,base,methods in [('app/chain/storage.py','StorageChain','MediaChain',['download_file']),('app/modules/filemanager/__init__.py','FileManagerModule',None,['download_file']),('app/modules/filemanager/storages/local.py','LocalStorage',None,['download'])]:
 owner=one(path,ast.ClassDef,name);run([ast.ClassDef(name=name,bases=[ast.Name(id=base,ctx=ast.Load())]if base else[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in methods],decorator_list=[])],'<fixed '+name+'>')
for name in ['download','image']:
 node=one('app/api/endpoints/storage.py',ast.FunctionDef,name);node.decorator_list=[];run([node],'<fixed endpoint '+name+'>')
Chain=ns['StorageChain'];rows=[]
with tempfile.TemporaryDirectory()as tmp:
 folder=Path(tmp);(folder/'native.txt').write_bytes(b'owned native content');(folder/'override.txt').write_bytes(b'owned plugin content')
 for action in ['download','image']:
  for name,value,fail in [('none',None,False),('override','override.txt',False),('false',False,False),('empty_list',[],False),('list',['bad'],False),('dict',{},False),('throw',None,True),('missing','missing.txt',False)]:
   calls=[];errors=[]
   def callback(fileitem,path=None):
    calls.append('source')
    if fail:raise RuntimeError('owned download failure')
    return folder/value if isinstance(value,str)else copy.deepcopy(value)
   def clone(fileitem,path=None):calls.append('clone');return None
   local=ns['LocalStorage']();core=ns['FileManagerModule']();core._support_storages=['local'];core.get_priority=lambda:0;core.get_name=lambda:'文件整理'
   def storage(name):calls.append('native');return local
   core._FileManagerModule__get_storage_oper=storage
   chain=Chain();chain.pluginmanager=NS(get_plugin_modules=lambda:{('source','source'):{'download_file':callback},('clone','clone'):{'download_file':clone}});chain.modulemanager=NS(get_running_modules=lambda name:[core]);chain.messagehelper=NS(put=lambda **kw:errors.append(kw));chain.eventmanager=NS(send_event=lambda*a:None);ns['StorageChain']=lambda:chain
   body=None;success=None;error=None
   try:
    result=ns[action](FileItem(storage='local',type='file',path=str(folder/'native.txt'),name='native.txt'))
    if isinstance(result,FileResponse):
     messages=[]
     async def send(message):messages.append(message)
     async def receive():return {'type':'http.request'}
     asyncio.run(result({'type':'http','method':'GET','headers':[]},receive,send));body=b''.join(m.get('body',b'')for m in messages).decode()
    elif isinstance(result,Response):body=result.body.decode()
    else:success=result.success
   except Exception as e:error=type(e).__name__
   rows.append(dict(action=action,name=name,plugin=value,fail=fail,calls=calls,body=body,success=success,error=error,messages=len(errors)))
Path(__file__).with_name('browser-download-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'source_sha256':hashes,'boundary':__doc__,'starlette_adapter_version':starlette.__version__,'cases':rows},ensure_ascii=False,indent=2)+'\n');print(len(rows),'fixed storage download/image cases')
