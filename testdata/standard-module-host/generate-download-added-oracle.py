"""Actual fixed background submission and ordinary download_added module wrapper.
ThreadHelper queue and installed module registries are owned boundaries; the
fixed closure, dispatcher None/list/scalar/exception behavior run unchanged.
"""
import ast,copy,json
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-downloader-add-oracle.py');code=p.read_text().split("chain_path=root/'app/chain/download.py'")[0].replace('"stop_torrents", "remove_torrents", "download"','"stop_torrents", "remove_torrents", "download", "download_added"');scope={'__file__':str(p)};exec(compile(code,str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];clean=scope['clean']
owner=next(n for n in ast.parse((root/'app/chain/download.py').read_text()).body if isinstance(n,ast.ClassDef)and n.name=='DownloadChain');fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='_submit_download_added_task'))
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),fn],type_ignores=[])),'<fixed:download-added background closure>','exec'),ns)
context=json.loads(Path('testdata/standard-event-target/resource-download-oracle.json').read_text())['cases'][0]['context'];cases=[]
for name,returned,callback_error,submit_error in [('none',None,False,False),('false',False,False,False),('list',[],False,False),('scalar',True,False,False),('callback_error',None,True,False),('submit_error',None,False,True)]:
 queue=[];calls=[];errors=[];roles=[]
 def submit(call):
  if submit_error:raise RuntimeError('owned submit failure')
  queue.append(call)
 ns['ThreadHelper']=lambda:SimpleNamespace(submit=submit);ns['logger']=SimpleNamespace(info=lambda*a:None,debug=lambda*a:None,error=lambda *a:errors.append(str(a)))
 def callback(context,download_dir,torrent_content=None):
  calls.append(clean({'context':context,'download_dir':download_dir,'torrent_content':torrent_content}))
  if callback_error:raise RuntimeError('owned callback failure')
  return returned
 chain=ns['ChainBase']();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'download_added':callback}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda method:[]);chain.messagehelper=SimpleNamespace(put=lambda **kw:roles.append(kw['role']));chain.eventmanager=SimpleNamespace(send_event=lambda*a:None)
 ns['_submit_download_added_task'](chain,context=context,download_dir=Path('/owned/downloads'),torrent_content=b'owned\x00\xff')
 before=len(calls)
 for call in queue:call()
 cases.append({'name':name,'result':returned,'callback_error':callback_error,'submit_error':submit_error,'calls_before_drain':before,'calls':calls,'errors':len(errors),'roles':roles})
Path(__file__).with_name('download-added-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/download.py:447-470','app/chain/download.py:1014-1018','app/chain/__init__.py:1391-1411'],'kind':__doc__,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual fixed post-Add background cases')
