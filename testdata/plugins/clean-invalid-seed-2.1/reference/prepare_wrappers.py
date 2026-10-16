import json,pathlib,subprocess,shlex
root=pathlib.Path(__file__).parent
source=json.loads((root/'native-wrapper-methods.json').read_text())
cases=[]
for kind in ('qbittorrent','transmission'):
 for op in ('delete','tags'):
  for throw in (False,True):
   for value in ('normal','empty','duplicates','unhashable'):
    cases.append({'id':kind+'_'+op+'_'+str(throw)+'_'+value,'kind':kind,'op':op,'throw':throw,'hash':'' if value=='empty' else 'a'*40,'tags':['mark','own','mark'] if value=='duplicates' else [['invalid-list']] if value=='unhashable' else ['mark']})
script=r'''import ast,json,sys,types
from typing import *
p=json.load(sys.stdin);trace=[]
class Quiet:
 def __getattr__(self,n):return lambda *a,**kw:None
class Client:
 def __getattr__(self,n):
  def call(**kw):
   if n=='change_torrent':kw['labels']=sorted(kw['labels'],key=lambda x:json.dumps(x,sort_keys=True))
   trace.append({'method':n,'args':kw})
   if current['throw']:raise RuntimeError('synthetic RPC failure')
   return None
  return call
classes={}
for key,kind in [('app/modules/qbittorrent/qbittorrent.py','qbittorrent'),('app/modules/transmission/transmission.py','transmission')]:
 nodes=[ast.parse(v).body[0] for k,v in p['source'][key]['methods'].items() if k in ('delete_torrents','set_torrent_tag','set_torrents_tag')]
 c=ast.ClassDef(name='Wrapper',bases=[],keywords=[],body=nodes,decorator_list=[]);tree=ast.fix_missing_locations(ast.Module([c],[]));env={**globals(),'logger':Quiet()};exec(compile(tree,'<installed-downloader-method-AST>','exec'),env);classes[kind]=env['Wrapper']
out=[]
for current in p['cases']:
 trace=[];w=classes[current['kind']]();w.qbc=Client();w.trc=Client()
 try:
  if current['op']=='delete':r=w.delete_torrents(False,current['hash'])
  elif current['kind']=='qbittorrent':r=w.set_torrents_tag(current['hash'],current['tags'])
  else:r=w.set_torrent_tag(current['hash'],current['tags'])
  e=None
 except Exception as err:r=None;e=type(err).__name__
 out.append({'input':current,'expected':{'result':r,'error':e,'trace':trace}})
json.dump(out,sys.stdout)'''
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -c '+shlex.quote(script)],input=json.dumps({'source':source,'cases':cases}),text=True,capture_output=True,check=True)
(root/'wrapper-oracle.json').write_text(json.dumps(json.loads(r.stdout),ensure_ascii=False,indent=2)+'\n');print(len(cases))
