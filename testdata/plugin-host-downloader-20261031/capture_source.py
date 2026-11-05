"""Execute fixed MoviePilot wrapper AST against owned protocol objects only."""
import ast, hashlib, json, pathlib, subprocess, types
ROOT=pathlib.Path(__file__).resolve().parent
SOURCE='/Users/accidia/projects/MoviePilot'
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
class Logger:
 def error(self,*args): pass
out={'source_commit':COMMIT,'kind':'isolated fixed-source AST replay; no application imports or production endpoints','sources':{},'cases':[],'faults':[],'lookups':[],'tr_tag_cases':[],'tr_limit_cases':[],'qb_add_cases':[],'downloading_cases':[]}
for kind,path in [('qbittorrent','app/modules/qbittorrent/qbittorrent.py'),('transmission','app/modules/transmission/transmission.py')]:
 raw=subprocess.check_output(['git','-C',SOURCE,'show',f'{COMMIT}:{path}'])
 tree=ast.parse(raw)
 cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and any(isinstance(m,ast.FunctionDef) and m.name=='get_completed_torrents' for m in n.body))
 wanted=['get_torrents','get_completed_torrents','get_downloading_torrents','start_torrents','stop_torrents','delete_torrents','recheck_torrents']
 if kind=='qbittorrent': wanted+=['delete_torrents_tag','remove_torrents_tag','set_torrents_tag','set_torrent_category','get_torrent_id_by_tag','__get_last_add_torrentid_by_tag','add_torrent']
 else: wanted+=['set_torrent_tag','change_torrent']
 methods=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in wanted]
 out['sources'][kind]={'path':path,'sha256':hashlib.sha256(raw).hexdigest(),'methods':{m.name:m.lineno for m in methods}}
 for m in methods:
  m.returns=None
  for arg in m.args.args+m.args.kwonlyargs: arg.annotation=None
  if m.args.vararg: m.args.vararg.annotation=None
  if m.args.kwarg: m.args.kwarg.annotation=None
 module=ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Wrapper',bases=[],keywords=[],body=methods,decorator_list=[])],type_ignores=[]))
 namespace={'logger':Logger(),'time':types.SimpleNamespace(sleep=lambda seconds:None)};exec(compile(module,path,'exec'),namespace)
 wrapper=namespace['Wrapper']();wrapper._trarg=[]
 if kind=='qbittorrent':
  states=['uploading','pausedUP','queuedUP','stalledUP','forcedUP','allocating','checkingDL','checkingUP','downloading','error','forcedDL','missingFiles','metaDL','moving','queuedDL','stalledDL','unknown','']
  rows=[{'hash':f'{i+1:040x}','state':state,'progress':1,'amount_left':0,'tags':' alpha, beta '} for i,state in enumerate(states)]
  rows += [{'hash':f'{101:040x}','state':'pausedUP','progress':0.9,'amount_left':0,'tags':'alpha'}, {'hash':f'{102:040x}','state':'pausedUP','progress':1,'amount_left':1,'tags':'alpha'}]
  class Client:
   def torrents_info(self,**kwargs):
    self.last=kwargs;return [dict(row) for row in rows if kwargs.get('torrent_hashes') is None or row['hash'] in kwargs['torrent_hashes']]
  wrapper.qbc=Client()
 else:
  names={0:'stopped',1:'check_pending',2:'checking',3:'download_pending',4:'downloading',5:'seed_pending',6:'seeding'}
  rows=[{'hashString':f'{201+i:040x}','status':i,'labels':[' alpha ','beta']} for i in names]
  class Client:
   def get_torrents(self,**kwargs):
    self.last=kwargs;return [types.SimpleNamespace(**dict(row,status=names[row['status']])) for row in rows if kwargs.get('ids') is None or row['hashString'] in kwargs['ids']]
  wrapper.trc=Client()
 for tags in [None,['alpha'],['alpha','beta'],['absent']]:
  result=wrapper.get_completed_torrents(tags=tags)
  hashes=[r['hash'] if isinstance(r,dict) else r.hashString for r in result]
  out['cases'].append({'kind':kind,'tags':tags,'rows':rows,'expected_hashes':hashes})


 for tags in [None,['alpha'],['alpha','beta'],['absent']]:
  result=wrapper.get_downloading_torrents(tags=tags)
  hashes=[r['hash'] if isinstance(r,dict) else r.hashString for r in result]
  out['downloading_cases'].append({'kind':kind,'tags':tags,'rows':rows,'expected_hashes':hashes,'source_request':wrapper.qbc.last if kind=='qbittorrent' else wrapper.trc.last})
 if kind=='qbittorrent':
  rows=[{'hash':'f'*40,'state':'downloading','progress':0,'amount_left':1}]
  result=wrapper.get_completed_torrents(tags=['required'])
  out['faults'].append({'kind':kind,'name':'malformed_tags_before_completion_filter','rows':rows,'expected_error':result is None})
 else:
  rows=[{'hashString':'f'*40,'status':4,'labels':None}]
  result=wrapper.get_completed_torrents(tags=['required'])
  out['faults'].append({'kind':kind,'name':'incomplete_status_before_malformed_labels','rows':rows,'expected_error':result is None,'expected_count':len(result or [])})
 # Source control oracle records actual delegated method and arguments.
 class Calls:
  def __getattr__(self,name):
   def call(**kwargs): self.calls.append({'method':name,'arguments':kwargs})
   return call
  def __bool__(self):return True
 client=Calls();client.calls=[]
 if kind=='qbittorrent': wrapper.qbc=client
 else: wrapper.trc=client
 hashes=['a'*40,'b'*40]
 wrapper.start_torrents(hashes);wrapper.stop_torrents(hashes);wrapper.recheck_torrents(hashes);wrapper.delete_torrents(False,hashes);wrapper.delete_torrents(True,hashes)
 if kind=='qbittorrent':
  wrapper.set_torrents_tag(hashes,['owned']);wrapper.remove_torrents_tag(hashes,['owned']);wrapper.delete_torrents_tag(hashes,['owned']);wrapper.set_torrent_category(hashes[0],'owned')
 out['sources'][kind]['control_calls']=client.calls
 if kind=='qbittorrent':
  wrapper._category=True;wrapper._sequentail=False;wrapper._first_last_piece=False
  wrapper._Wrapper__parse_add_torrent_response=lambda response:(True,[])
  for paused in [False,True]:
   client.calls=[];wrapper.add_torrent(b'owned material',is_paused=paused,download_dir='/owned/downloads',tag=['owned'],upload_limit=1024,download_limit=2048)
   calls=json.loads(json.dumps(client.calls,default=lambda value:{'bytes_hex':value.hex()}))
   out['qb_add_cases'].append({'paused':paused,'calls':calls})
  for present in [True,False]:
   sleeps=[];calls=[];cleanup=[]
   namespace['time'].sleep=lambda seconds:sleeps.append(seconds)
   wrapper.get_torrents=lambda **kwargs:(calls.append(kwargs) or ([{'hash':'a'*40}] if present else []),False)
   wrapper.delete_torrents_tag=lambda *args:cleanup.append(args) or False
   result=wrapper.get_torrent_id_by_tag(['owned'],status='paused')
   out['lookups'].append({'present':present,'sleeps':sleeps,'reads':calls,'cleanup':cleanup,'hash':result})
 else:
  for up,down in [(123,0),(0,64),(-1,None)]:
   client.calls=[];result=wrapper.change_torrent('a'*40,upload_limit=up,download_limit=down)
   out['tr_limit_cases'].append({'upload_limit_kb':up,'download_limit_kb':down,'accepted':result,'calls':client.calls})
  for tags,org in [(['new','new'],['existing','new']),([],['existing'])]:
   client.calls=[];result=wrapper.set_torrent_tag('a'*40,tags,org)
   calls=client.calls
   for call in calls:
    if 'labels' in call['arguments']:call['arguments']['labels']=sorted(call['arguments']['labels'])
   out['tr_tag_cases'].append({'tags':tags,'original_tags':org,'accepted':result,'calls':calls,'normalization':'Python set order is unspecified; compare sorted labels only'})
(ROOT/'source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
