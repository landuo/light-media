"""Read-only original fallback translator; owned filesystem and recording handler."""
import ast,datetime,hashlib,json,os,pathlib,shutil,sys,tempfile,threading,types,typing
source=pathlib.Path('/app/app/plugins/scrapefileclean/__init__.py').read_bytes();assert hashlib.sha256(source).hexdigest()=='c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af'
tree=ast.parse(source);handler=next(n for n in tree.body if isinstance(n,ast.Try)).handlers[0];classes=[n for n in handler.body if isinstance(n,ast.ClassDef)]
ns=dict(typing.__dict__);ns.update(threading=threading,Path=pathlib.Path,os=os,logger=types.SimpleNamespace(warning=lambda*a:None,debug=lambda*a:None));exec(compile(ast.fix_missing_locations(ast.Module(body=classes,type_ignores=[])),'<actual installed fallback>','exec'),ns)
cases=[dict(name='existing-rename',files=['a'],batches=[dict(move=['a','b'],changes=[['deleted','a'],['added','b']])]),dict(name='two-batches',files=['a'],batches=[dict(move=['a','b'],changes=[['added','b'],['deleted','a']]),dict(move=['b','c'],changes=[['deleted','b'],['added','c']])]),dict(name='unpaired-delete',files=['a'],batches=[dict(remove='a',changes=[['deleted','a']])]),dict(name='missing-initial',files=[],batches=[dict(changes=[['deleted','a']])]),dict(name='missing-added-stat',files=[],batches=[dict(changes=[['added','a']])]),dict(name='modified-ignored',files=['a'],batches=[dict(changes=[['modified','a']])]),dict(name='directory-delete-is-file',files=['a'],batches=[dict(changes=[['deleted','directory']])]),dict(name='temp-rename',files=['a'],batches=[dict(move=['a','a.tmp'],changes=[['deleted','a'],['added','a.tmp']])])]
results=[]
for case in cases:
 with tempfile.TemporaryDirectory(prefix='scrape-fallback-owned-')as tmp:
  root=pathlib.Path(tmp)
  for name in case['files']:(root/name).write_text('owned')
  trace=[]
  class Handler:
   def on_created(self,event):trace.append(dict(Kind='created',Src=event.src_path,Dest='',Directory=event.is_directory))
   def on_deleted(self,event):trace.append(dict(Kind='deleted',Src=event.src_path,Dest='',Directory=event.is_directory))
   def on_moved(self,event):trace.append(dict(Kind='moved',Src=event.src_path,Dest=event.dest_path,Directory=event.is_directory))
  def watch(*a,**kw):
   for batch in case['batches']:
    if 'move'in batch:(root/batch['move'][0]).rename(root/batch['move'][1])
    if 'remove'in batch:(root/batch['remove']).unlink()
    yield[(kind,str(root/name))for kind,name in batch['changes']]
  previous=sys.modules.get('watchfiles');sys.modules['watchfiles']=types.SimpleNamespace(Change=types.SimpleNamespace(added='added',deleted='deleted'),watch=watch)
  try:ns['_WatchfilesObserver']()._watch(Handler(),str(root),True)
  finally:
   if previous is None:sys.modules.pop('watchfiles',None)
   else:sys.modules['watchfiles']=previous
  results.append(dict(case=case,trace=[{key:value.replace(tmp,'/fixture')if isinstance(value,str)else value for key,value in row.items()}for row in trace]))
print(json.dumps(dict(source_sha256=hashlib.sha256(source).hexdigest(),scope='original _WatchfilesObserver._watch AST; fake watch() ordered changes and recording event handler; owned files only',cases=results),ensure_ascii=False,indent=2))
