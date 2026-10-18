# Executed inside source_oracle.py with the actual captured class namespace.
import copy
flow_cases=[]
content=b'd4:infod4:name1:xee';actual_hash=core.info_hash_from_torrent(content)
for mode in ['success','false','error','missing_link','bad_hash','metadata_missing','existing_complete','existing_incomplete','target_seeding','target_paused']:
 config={'enabled':True,'notify':False,'token':'fixture-token','target_site_id':1,'source_site_ids':[2],'rss_urls':'https://target.test/rss','downloader':'qb'}
 p=Plugin();p.init_plugin(config);p._now=lambda:'2026-10-18T01:02:03+00:00'
 target=mode.startswith('target_');hash_value=actual_hash if target else 'a'*40
 job={'target_hash':hash_value,'target_site_id':1,'title':'Fixture','state':'AWAITING_SELECTION','probes':[],'candidates':[]}
 data={'title':'Source','page_url':'https://source.test/details.php?id=7&passkey=fixture','enclosure':'https://source.test/download.php?id=7&passkey=fixture','site_cookie':'fixture-cookie','site_ua':'FixtureUA','site_proxy':False}
 if mode=='missing_link':data.pop('enclosure')
 candidate=core.SourceCandidate(core.IyuuMatch(2,'7',('b'*40 if mode=='bad_hash' else actual_hash)),1 if target else 2,'Source',3,0,0,SimpleNamespace(**data))
 calls=[]
 def add(**kw):
  calls.append({'kind':'add','intent':copy.deepcopy(p.data['jobs'][hash_value]),'category':kw['category'],'tag':kw['tag'],'download_dir':kw['download_dir'],'content_hex':kw['content'].hex()})
  if mode=='error':raise RuntimeError('synthetic lost ack')
  return mode!='false'
 namespace['DownloaderHelper']=lambda:SimpleNamespace(get_service=lambda **kw:SimpleNamespace(instance=SimpleNamespace(add_torrent=add)))
 namespace['TorrentHelper']=lambda:SimpleNamespace(download_torrent=lambda **kw:(None,None if mode=='metadata_missing' else content,None,None,'fixture metadata error'))
 namespace['settings']=SimpleNamespace(USER_AGENT='FixtureUA')
 p._get_torrent=lambda h: ({'state':('pausedUP' if mode=='target_paused' else 'uploading'),'progress':1,'amount_left':0} if mode in ['existing_complete','target_seeding','target_paused'] else {'state':'downloading','progress':.4,'amount_left':8} if mode=='existing_incomplete' else None)
 try:
  ok,reason=p._start_source(candidate,job);out={'ok':ok,'reason':reason,'job':job,'calls':calls}
 except Exception as e:out={'error':type(e).__name__,'job':job,'calls':calls}
 flow_cases.append({'mode':mode,'value':copy.deepcopy(out)})
(ROOT/'flow-oracle.json').write_text(json.dumps({'cases':flow_cases,'source':'actual _start_source and _persist_job/_finish/_set_source_job methods','environment':{'python':sys.version}},ensure_ascii=False,indent=2)+'\n')
