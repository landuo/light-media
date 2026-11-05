"""Finish the owned Linux process after real browser writes; no production IO."""
import argparse,hashlib,http.cookiejar,json,os,pathlib,signal,socket,subprocess,time,urllib.error,urllib.request
p=argparse.ArgumentParser();p.add_argument('--root',required=True);p.add_argument('--driver-pid',type=int,required=True);a=p.parse_args();root=pathlib.Path(a.root).resolve();run=root/'run';out=root/'final';out.mkdir(exist_ok=False)
origin=json.loads((run/'browser-state.json').read_text())['origin'];port=origin.rsplit(':',1)[1]
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(method,path,value=None,status=200):
 raw=json.dumps(value,ensure_ascii=False).encode() if value is not None else None
 req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json','Origin':origin},method=method)
 try:r=client.open(req,timeout=15)
 except urllib.error.HTTPError as e:r=e
 data=r.read();assert r.code==status,(method,path,r.code,status,data[:500]);return json.loads(data) if data else None
def login():request('POST','/api/auth/login',dict(username='owned-message',password='owned-message-browser-password'))
def snap(instance):return request('GET','/api/v1/plugin/'+instance+'/snapshot')
def events(value,kind):return [r['value']['data'] for r in value['trace'] if r['kind']=='event' and r['value']['type']==kind]
def parsed_payload(config):return json.loads(config['body'])
instances=['OwnedHostMessage','OwnedHostMessagecopy'];process=None;log=None
try:
 login();before={i:snap(i) for i in instances}
 assert parsed_payload(before[instances[0]]['config'])['info']['text']=='browser ordinary recovered'
 assert parsed_payload(before[instances[1]]['config'])['info']['text']=='browser clone independent'
 assert before[instances[0]]['config']['source']=='Owned' and before[instances[1]]['config']['source']=='OwnedClone'
 assert all(v['config']['run_once'] is False for v in before.values())
 assert before[instances[0]]['pid']!=before[instances[1]]['pid']
 # Exactly the browser-written payload's hash reached each configured parser.
 for i in instances:
  value=before[i];info=parsed_payload(value['config'])['info'];digest=hashlib.sha256(value['config']['body'].encode()).hexdigest()
  assert any(r['kind']=='parse' and r['body_sha256']==digest and r['source']==value['config']['source'] for r in value['trace'])
  assert any(r['text']==info['text'] and r['source']==info['source'] and r['userid']==info['userid'] for r in events(value,'user.message'))
 # Each failed parse segment stops before producing an ordinary message.
 failures=[]
 for i in instances:
  trace=before[i]['trace'];fail_digest=hashlib.sha256(b'{"mode":"fail"}').hexdigest()
  for n,row in enumerate(trace):
   if row['kind']=='parse' and row['body_sha256']==fail_digest:
    end=next((k for k in range(n+1,len(trace)) if trace[k]['kind']=='parse'),len(trace))
    segment=trace[n+1:end]
    assert not any(r['kind']=='event' and r['value']['type']=='user.message' and r['value']['data']['source']=='Owned' for r in segment)
    failures.append(dict(instance=i,parse_index=n,segment_size=len(segment)))
 assert failures,'browser parser failure missing from actual trace'
 history=request('GET','/api/message/web');assert any(v['text']=='browser ordinary recovered' and v['action']==0 for v in history['data'])
 logs=[json.loads(line) for line in (run/'server.log').read_text().splitlines() if line.startswith('{')]
 errors=[r for r in logs if r.get('event') in ['module_execution_failed','incoming_message_failed']]
 assert any(r['event']=='incoming_message_failed' for r in errors)
 assert any(r['event']=='module_execution_failed' for r in errors)
 assert all('owned parser failure' not in str(r) and '"mode"' not in str(r) for r in errors)
 (out/'before-cold.json').write_text(json.dumps(dict(snapshots=before,history=history,parser_failure_segments=failures,safe_error_logs=errors),ensure_ascii=False,indent=2)+'\n')
 # Controlled handoff: the original driver persists its five completed stage
 # observations under KeyboardInterrupt, then gracefully stops its server.
 os.kill(a.driver_pid,signal.SIGINT)
 for _ in range(200):
  try:request('GET','/api/auth/status')
  except (OSError,TimeoutError):break
  time.sleep(.1)
 else:raise AssertionError('original owned server did not stop during handoff')
 assert (run/'failure.json').exists(),'interrupted driver did not retain stage observations'
 handoff=json.loads((run/'failure.json').read_text());assert handoff['error'].startswith('KeyboardInterrupt') and len(handoff['observations'])==5
 (out/'initial-driver-handoff.json').write_text(json.dumps(handoff,ensure_ascii=False,indent=2)+'\n')
 log=(out/'server-cold.log').open('wb')
 process=subprocess.Popen([str(root/'light-media-server-linux'),'--data-dir',str(run/'data'),'--listen','127.0.0.1:'+port],stdout=log,stderr=log)
 for _ in range(600):
  try:request('GET','/api/auth/status');break
  except (OSError,AssertionError):assert process.poll() is None;time.sleep(.02)
 else:raise AssertionError('cold owned server did not become ready')
 login();cold={i:snap(i) for i in instances}
 for i in instances:
  assert cold[i]['pid']!=before[i]['pid'] and cold[i]['config']==before[i]['config'] and cold[i]['trace']==before[i]['trace'],('cold replay/state drift',i)
 (out/'after-cold.json').write_text(json.dumps(cold,ensure_ascii=False,indent=2)+'\n')
 # No browser, queue replay or implicit one-shot invocation is needed to read
 # either actual installed worker after cold restoration.
 uninstalled=[]
 for i in reversed(instances):
  result=request('POST','/api/plugins/'+i+'/uninstall');assert result['data']['success']
  request('GET','/api/v1/plugin/'+i+'/snapshot',status=404);uninstalled.append(i)
 process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
 result=dict(status='passed',classification='real Linux cmd/server + separately installed compiled standard SDK + actual browser writes; synthetic channel messages, no external messenger service',source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',browser_persisted_source=parsed_payload(before[instances[0]]['config']),browser_persisted_clone=parsed_payload(before[instances[1]]['config']),before_worker_pids={i:before[i]['pid'] for i in instances},cold_worker_pids={i:cold[i]['pid'] for i in instances},cold_trace_and_config_equal=True,zero_one_shot_replay=True,parser_failure_segments=failures,safe_error_log_count=len(errors),initial_driver_interruption='intentional control handoff; original failed/KeyboardInterrupt record retained, not relabeled as passed',uninstalled_api404=uninstalled,stopped=True)
 (out/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(json.dumps(result,ensure_ascii=False),flush=True)
except BaseException as error:
 (out/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+': '+str(error)),ensure_ascii=False,indent=2)+'\n');raise
finally:
 if process is not None:process.send_signal(signal.SIGTERM);process.wait(timeout=20)
 if log:log.close()
