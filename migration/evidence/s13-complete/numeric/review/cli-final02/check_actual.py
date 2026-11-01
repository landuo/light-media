"""Read fixed owned CLI artifacts and hash inputs only; never runs any program."""
from pathlib import Path
import json,hashlib,collections,datetime
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');A=R/'release-light-media';C=R/'target-drafts/s13-tr-numeric01/cli02';E=R/'test-instances/s13-tr-numeric-normal02';O=R/'audits/s13-tr-numeric-independent/cli-final02'
def load(p):return json.loads(p.read_bytes())
def rec(p):b=p.read_bytes();return dict(path=str(p),bytes=len(b),sha256=hashlib.sha256(b).hexdigest())
def typed(x):
 if isinstance(x,dict):return ('dict',sorted((k,typed(v)) for k,v in x.items()))
 if isinstance(x,list):return ('list',[typed(v) for v in x])
 return (type(x).__name__,x)
def check(p,v):r=rec(p);assert all(r[k]==v[k] for k in ('bytes','sha256')),str(p);return r
r=load(E/'result.json');tool=load(E/'coordinator-tool-receipt.json');binding=load(C/'execution-bindings.json');source=load(R/'evidence/s13-tr-numeric-source01/stdout.json');cases={c['id']:c for c in source['cases']}
assert r['status']=='PASS' and (E/'driver.exit').read_text()=='0\n' and tool['exit']==0 and tool['session']==85269
assert tool['json_files']['result.json']==rec(E/'result.json')['sha256']=='2fb5b908091a8738374c4c552c1043051e0a7331f4afe0b363b99936e1d8f6f9'
for name,h in tool['json_files'].items():assert rec(E/name)['sha256']==h
assert r['bindings_sha256']==rec(C/'execution-bindings.json')['sha256']=='2718241ff25ac201c433c6e5e0b187e85f5f53bfad622f6424846a5c93bb4c27'
for x in binding['inputs']:check(Path(x['path']),x)
assert len(binding['inputs'])==6406 and r['input_drift']==[] and r['environment_unchanged'] is True
assert not any(r.get(k) for k in ['error','cleanup_error','forced_worker_cleanup','peer_errors','terminal_reserve_error'])
protected=load(R/'target-drafts/s13-tr-numeric01/native01/clean-view/protected-active-files.json')['files']
for x in protected:check(Path(x['path']),x)
raws=[];business=[];logins=0
api='/api/v1/plugin/OwnedTRFinal/tr-final'
for call in r['calls']:
 assert call['started_monotonic']<=call['ended_monotonic'] and call['status'] in [200,405]
 assert (call['method'],call['path']) in [('GET','/healthz'),('POST','/api/auth/login'),('GET','/api/plugins/markets'),('POST','/api/plugins/install'),('GET',api),('POST',api)]
 if call['path']=='/api/auth/login':
  assert call['private_headers_body_not_retained'] is True and 'raw_file' not in call;logins+=1;continue
 p=E/call['raw_file'];assert p.parent==E and rec(p)['sha256']==call['sha256'];raws.append(rec(p));value=load(p)
 if call['path']=='/api/plugins/install':assert value['data']['success'] is True
 if call['path']=='/api/plugins/markets':assert value['data']['plugin_statistic_share'] is False and value['data']['plugin_server_url']==''
 if call['method']=='POST' and call['path']==api:business.append((call,value))
assert len(business)==5 and len(raws)==12 and logins==2 and len(r['calls'])==14
peers=r['peer_calls'];counts=dict(collections.Counter(p['method']+' '+p['path'] for p in peers));assert counts==r['actual_endpoint_request_counts']=={'GET /index':1,'GET /worker':1,'POST /transmission/rpc':8}
assert len(peers)==10 and all(p['status']==200 and p['response_completed'] is True and p['reply_monotonic']<=p['completed_monotonic'] for p in peers)
assert [p['path'] for p in peers[:2]]==['/index','/worker'] and peers[1]['response_bytes']==Path(R/'runtime/s13-tr-numeric-fixture02').stat().st_size
ids=['numeric-underscores-all-settings','int-adjacent-underscores','numeric-unicode-nd-and-space','numeric-float-exponent-underscores','numeric-unicode-nd-and-space'];cursor=2;projections=[]
for i,(q,(call,response),case_id) in enumerate(zip(r['queries'],business,ids)):
 assert typed(response)==typed(q['response']) and call['host_pid']==q['host_pid']
 assert q['peer_entry_start']==cursor
 original=cases[case_id];s=response;attempt=s['results'][0]
 assert s['requested']==s['completed']==1 and len(s['results'])==1 and s['init_count']==1
 assert attempt['kind']=='transmission' and attempt['op']=='change' and attempt['downloader_id']=='owned-tr-final-tr'
 hc=attempt['host_calls'];assert (hc['total'],hc['succeeded'],hc['failed'])==(1,1,0) and hc['input_json_bytes']>0 and hc['output_json_bytes']>0
 if original['outcome']['kind']=='uncaught_exception':
  assert original['outcome']['class']=='ValueError' and s['ok'] is False and s['succeeded']==0 and s['failed']==1
  assert attempt['ok'] is False and attempt['source_error']=='ValueError' and attempt['error_code']=='source_exception' and 'result' not in attempt
  assert q['peer_entry_end']==cursor and q['selected_delta']=={}
  assert not any(call['started_monotonic']<=p['reply_monotonic']<=call['ended_monotonic'] for p in peers)
 else:
  assert original['outcome']['kind']=='return' and original['outcome']['value'] is True
  assert s['ok'] is True and s['succeeded']==1 and s['failed']==0 and attempt['ok'] is True and attempt['result'] is True and attempt['source_truthy'] is True and attempt['result_kind']=='source_bool'
  assert q['peer_entry_end']==cursor+2 and q['selected_delta']=={case_id:1}
  pair=peers[cursor:cursor+2];assert [p['rpc_method'] for p in pair]==['session-get','torrent-set'] and pair[0]['arguments']=={}
  assert all(call['started_monotonic']<=p['reply_monotonic']<=p['completed_monotonic']<=call['ended_monotonic'] for p in pair)
  oc=[x for x in original['trace'] if x['event']=='recording_client_call'];assert len(oc)==1 and oc[0]['kwargs']['ids']=='owned-numeric'
  want=dict(oc[0]['kwargs']);want['ids']=['a'*40];assert typed(pair[1]['arguments'])==typed(want)
  for k,kind in oc[0]['python_types'].items():
   if k!='ids':assert type(pair[1]['arguments'][k]) is {'int':int,'float':float,'bool':bool}[kind]
  cursor+=2
 projections.append(dict(case_id=case_id,worker=s['pid'],host=q['host_pid'],init=s['init_count'],invocation=s['invocation'],ok=s['ok'],peer_start=q['peer_entry_start'],peer_end=q['peer_entry_end'],typed_peer='exact original values/types; fixed owned ids adaptation only'))
assert cursor==len(peers)
assert [q['response']['pid'] for q in r['queries']]==[41,41,41,41,68] and [q['response']['invocation'] for q in r['queries']]==[1,2,3,4,1]
assert len(r['processes'])==2 and [x['pid'] for x in r['processes']]==[25,53]
for host in r['processes']:
 assert host['exit']==0 and not host.get('forced_stop') and host['normal_not_safe'] is True and host['executable_sha256']==binding['server_sha256'] and host['owned_listener_inode'] and host['identity']['ppid']==2
 assert host['same_private_data']==str(E/'private-data')
assert len(r['worker_identities'])==5
for i,w in enumerate(r['worker_identities']):
 assert w['pid']==r['queries'][i]['response']['pid'] and w['ppid']==w['host_pid']==r['queries'][i]['host_pid'] and w['executable_sha256']==binding['worker_sha256']
 assert Path(w['executable'])==E/'private-data/plugin-packages/OwnedTRFinal/owned-tr-final-fixture'
 assert rec(Path(w['executable']))['sha256']==binding['worker_sha256']
assert len({(w['pid'],w['starttime']) for w in r['worker_identities']})==2
assert not any(p['method']=='GET' for p in peers[2:]) # no cold artifact fetch
manifest=load(E/'worker-manifest.json');assert manifest['plugin_id']=='OwnedTRFinal' and manifest['capabilities']==['downloader.transmission.set_tags','downloader.transmission.change']
assert load(E/'owned-plugin-log-projection.json')==[] and rec(E/'owned-plugin-log-projection.json')['sha256']==r['log_projection_sha256']
# Only the fixed owned log file, never DB/secret.key/cookies. Keep raw local.
p=E/'private-data/logs/app.jsonl';assert p.is_file() and not p.is_symlink() and p.stat().st_size<2*1024*1024
logs=[json.loads(x) for x in p.read_text().splitlines() if x];relevant=[x for x in logs if x.get('plugin_id')=='OwnedTRFinal' and x.get('event')=='downloader_command'];assert relevant==[]
resources=[json.loads(x) for x in (E/'resources.jsonl').read_text().splitlines()];assert resources and all(x['scratch_free']>=1<<30 and x['tmp_free']>=3*(1<<29) for x in resources)
out={'status':'ACCEPT_NUMERIC_ORDINARY_INSTALLED_CLI02','at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'result':rec(E/'result.json'),'tool':rec(E/'coordinator-tool-receipt.json'),'tool_contents':tool,'binding':rec(C/'execution-bindings.json'),'current_binding_inputs':len(binding['inputs']),'current_drift':[],'protected_Meta25_current_same':True,'raw_nonlogin_responses':raws,'projections':projections,'peer_counts':counts,'normal_hosts':r['processes'],'unique_workers':[r['worker_identities'][0],r['worker_identities'][-1]],'installed_worker_current_sha':binding['worker_sha256'],'source':rec(R/'evidence/s13-tr-numeric-source01/stdout.json'),'empty_command_log_projection':rec(E/'owned-plugin-log-projection.json'),'owned_log_sha_private_local_only':rec(p),'resource_samples':len(resources),'resource_file':rec(E/'resources.jsonl'),'retained_failure':rec(R/'audits/s13-tr-numeric-independent/native-attempt01/readback.json'),'limits':['No new /proc process probe; natural Wait/reap from bound driver assertions and terminal receipts','Inherited environment equality observed inside driver; no exported secret environment map','RPC decoded typed arguments and entity byte counts, not whole wire-byte ordering','Original source recording; no actual vendor/daemon/UI'], 'reviewer_execution':'read/hash JSON only; no source/Go/HTTP/DB'}
(O/'readback.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'status':out['status'],'inputs':len(binding['inputs']),'responses':len(raws),'queries':len(projections),'peers':counts,'resource_samples':len(resources)}))
