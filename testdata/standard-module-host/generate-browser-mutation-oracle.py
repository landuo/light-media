"""Fixed StorageChain/dispatcher/FileManager/LocalStorage and API mkdir/rename/delete.
Actual temporary local filesystem, synthetic plugin return values and effects.
Never imports or starts the reference application; production is Go only.
"""
import ast,copy,json,shutil,tempfile,typing
from pydantic import model_validator
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-browser-storage-oracle.py');env={'__file__':str(p)};exec(compile(p.read_text().split("base={")[0],str(p),'exec'),env)
ns,root,one,run,FileItem=env['ns'],env['root'],env['one'],env['run'],env['FileItem']
ns.update(shutil=shutil,model_validator=model_validator,LocaleHelper=SimpleNamespace(translate_text=lambda text,**kw:text,get_current_locale=lambda:None))
run([one('app/schemas/response.py',ast.ClassDef,'Response')],'<fixed:Response>');ns['Response'].model_rebuild(_types_namespace=ns);ns['schemas'].Response=ns['Response']
for path,name,base,methods in [('app/chain/storage.py','StorageChain','MediaChain',['create_folder','rename_file','delete_file']),('app/modules/filemanager/__init__.py','FileManagerModule',None,['create_folder','rename_file','delete_file']),('app/modules/filemanager/storages/local.py','LocalStorage',None,['__get_diritem','create_folder','rename','delete'])]:
 owner=one(path,ast.ClassDef,name);run([ast.ClassDef(name=name,bases=[ast.Name(id=base,ctx=ast.Load())]if base else[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in methods],decorator_list=[])],'<fixed:'+name+'>')
for name in ['mkdir','rename','delete']:
 node=one('app/api/endpoints/storage.py',ast.FunctionDef,name);node.decorator_list=[];run([node],'<fixed:endpoint:'+name+'>')
Chain=ns['StorageChain'];cases=[]
for action,method in [('mkdir','create_folder'),('rename','rename_file'),('delete','delete_file')]:
 for mode,value in [('none',None),('false',False),('empty_list',[]),('list',['plugin']),('true_no_effect',True),('effect_true',True),('effect_false',False),('effect_none',None),('effect_throw',None)]:
  with tempfile.TemporaryDirectory(prefix='owned-source-file-') as td:
   folder=Path(td);old=folder/'Owned.mkv';target=folder/'New';old.write_bytes(b'owned content');item=FileItem(storage='local',type='dir'if action=='mkdir'else'file',path=str(folder)+'/'if action=='mkdir'else str(old),name=folder.name if action=='mkdir'else old.name);calls=[];errors=[]
   def callback(**kwargs):
    calls.append({'role':'plugin','keys':sorted(kwargs),'name':kwargs.get('name')})
    if mode.startswith('effect_'):
     if action=='mkdir':target.mkdir()
     elif action=='rename':old.rename(target)
     else:old.unlink()
    if mode=='effect_throw':raise RuntimeError('owned failure after effect')
    return copy.deepcopy(value)
   owner=one('app/modules/filemanager/__init__.py',ast.ClassDef,'FileManagerModule');run([ast.ClassDef(name='FileManagerModule',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in ['create_folder','rename_file','delete_file']],decorator_list=[])],'<fixed:FileManagerModule>')
   local=ns['LocalStorage']();local.schema=SimpleNamespace(value='local');core=ns['FileManagerModule']();core._support_storages=['local'];core.get_priority=lambda:0;core.get_name=lambda:'文件整理';core._FileManagerModule__get_storage_oper=lambda storage:local
   ns['mutation_calls']=calls
   # Observe the unchanged source method signature and return annotation.
   # A **kwargs wrapper would invalidate ObjectUtils.check_signature.
   owner=one('app/modules/filemanager/__init__.py',ast.ClassDef,'FileManagerModule')
   method_node=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name==method)
   observer=ast.parse("mutation_calls.append({'role':'core','keys':sorted(k for k in locals() if k != 'self')})").body[0]
   method_node.body.insert(1,observer)
   observed=ast.ClassDef(name='FileManagerModule',bases=[],keywords=[],body=[method_node],decorator_list=[])
   run([observed],'<fixed:observed FileManagerModule>')
   setattr(core,method,getattr(ns['FileManagerModule'],method).__get__(core))
   chain=Chain();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{method:callback}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=SimpleNamespace(send_event=lambda*a:None);ns['StorageChain']=lambda:chain
   kwargs={'fileitem':item}
   if action=='mkdir':kwargs['name']='New'
   elif action=='rename':kwargs.update(new_name='New',recursive=False)
   result=ns[action](**kwargs)
   cases.append({'action':action,'method':method,'mode':mode,'value':value,'success':result.success,'source_exists':old.exists(),'target_exists':target.exists(),'core_calls':sum(c['role']=='core'for c in calls),'calls':calls,'error_titles':[e.get('title')for e in errors]})
Path(__file__).with_name('browser-mutation-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':__doc__,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual pinned source mutation cases')
