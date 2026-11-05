#!/usr/bin/env python3
"""Actual cmd/server + separately installed SDK lifecycle; no external channel.

The fixture supplies synthetic ordinary channel payloads. This does not claim
Lark service/webhook/business migration or delivery of an external message.
"""
import argparse, hashlib, http.cookiejar, http.server, json, os, pathlib, platform
import signal, socket, subprocess, threading, time, urllib.error, urllib.parse, urllib.request

p=argparse.ArgumentParser()
for name in ['server','worker','worker-v2','directory','oracle']:p.add_argument('--'+name,required=True)
p.add_argument('--hold',action='store_true')
p.add_argument('--listen',default='127.0.0.1')
a=p.parse_args();directory=pathlib.Path(a.directory).resolve();directory.mkdir(mode=0o700,parents=True,exist_ok=False)
workers={v:pathlib.Path(path).resolve() for v,path in [('1.0',a.worker),('2.0',a.worker_v2)]}
payloads={v:path.read_bytes() for v,path in workers.items()}
manifests={v:json.loads(subprocess.check_output([str(path),'--manifest'])) for v,path in workers.items()}
index=dict(schema_version=1,plugin_id='OwnedHostMessage',current_version='1.0',releases=[])
machine={'x86_64':'amd64','aarch64':'arm64','arm64':'arm64'}[platform.machine()]
def emit(handler,status,body):
    handler.send_response(status);handler.send_header('Content-Length',str(len(body)));handler.end_headers();handler.wfile.write(body)
class Repository(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path=urllib.parse.urlsplit(self.path).path
        if path=='/index':return emit(self,200,json.dumps(index).encode())
        if path.startswith('/worker/') and path.rsplit('/',1)[1] in payloads:return emit(self,200,payloads[path.rsplit('/',1)[1]])
        self.send_error(404)
    def log_message(self,*_):pass
repo=http.server.ThreadingHTTPServer(('127.0.0.1',0),Repository);threading.Thread(target=repo.serve_forever,daemon=True).start()
repo_origin='http://127.0.0.1:'+str(repo.server_port)
for version,payload in payloads.items():
    digest=hashlib.sha256(payload).hexdigest()
    index['releases'].append(dict(version=version,manifest=manifests[version],artifacts=[dict(platform=platform.system().lower()+'-'+machine,url=repo_origin+'/worker/'+version,format='executable',sha256=digest,executable_sha256=digest,size=len(payload),host_contract='light-media.sdk-resources/v1')]))
with socket.socket() as sock:sock.bind((a.listen,0));port=sock.getsockname()[1]
origin='http://'+a.listen+':'+str(port);data=directory/'data';env=dict(os.environ)
for name in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:env.pop(name,None)
bootstrap=dict(username='owned-message',password='owned-message-browser-password',settings=dict(plugin_markets=[repo_origin+'/index'],plugin_server_url='',media_recognize_share=False))
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
process=None;log=(directory/'server.log').open('wb');observations=[]
def request(method,path,value=None,status=200):
    raw=json.dumps(value,ensure_ascii=False).encode() if value is not None else None
    req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json'},method=method)
    try:response=client.open(req,timeout=25)
    except urllib.error.HTTPError as error:response=error
    raw=response.read();assert response.code==status,(method,path,response.code,status,raw[:700]);return json.loads(raw) if raw else None
def start():
    global process
    process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],env=env,stdout=log,stderr=log)
    for _ in range(600):
        try:request('GET','/api/auth/status');break
        except (OSError,AssertionError):assert process.poll() is None;time.sleep(.02)
    else:raise AssertionError('server startup timeout')
    request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop():
    global process
    if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
def snapshot(instance='OwnedHostMessage'):return request('GET','/api/v1/plugin/'+instance+'/snapshot')
def events(value,event):return [row['value']['data'] for row in value['trace'] or [] if row['kind']=='event' and row['value']['type']==event]
def wait_event(text,instance='OwnedHostMessage'):
    for _ in range(500):
        value=snapshot(instance)
        if any(row['text']==text for row in events(value,'user.message')):return value
        time.sleep(.02)
    raise AssertionError('ordinary event not observed: '+text)
def configure(instance,source,text,run_once=True,mode=None):
    payload=dict(mode=mode) if mode else dict(info=dict(userid=9007199254740993,username='owned-channel-user',channel='飞书',source=source,text=text,message_id='owned-message-id',chat_id='owned-chat',reply_to_message_id=42))
    config=dict(source=source,body=json.dumps(payload,ensure_ascii=False),run_once=run_once)
    value=request('PUT','/api/plugins/'+instance,config);assert value['data']['success'],value
    return config
try:
    start();value=request('POST','/api/plugins/install',dict(plugin_id='OwnedHostMessage',repo_url=repo_origin+'/index'));assert value['data']['success'],value
    configure('OwnedHostMessage','Owned','source ordinary')
    first=wait_event('source ordinary');assert first['config']['run_once'] is False,first
    original_event=next(row for row in events(first,'user.message') if row['text']=='source ordinary');assert original_event['userid']==9007199254740993,original_event
    assert all(row['kind']!='parse' or row['source']=='Owned' for row in first['trace'])
    observations.append(dict(case='install-config-once-parser-history-event',snapshot=first))
    configure('OwnedHostMessage','Owned','/owned_inbound argument')
    for _ in range(500):
        command=snapshot()
        if events(command,'owned.inbound.command'):break
        time.sleep(.02)
    else:raise AssertionError('registered ordinary command did not execute')
    assert events(command,'command.excute')[-1]['processing_status']['metadata']=={'owned':True}
    assert events(command,'owned.inbound.command')[-1]['arg_str']=='argument'
    observations.append(dict(case='ordinary-command',snapshot=command))
    history=request('GET','/api/message/web');assert any(row['userid']=='owned-channel-user' and row['action']==0 and row['channel']=='飞书' and row['text']=='source ordinary' for row in history['data']),history
    configure('OwnedHostMessage','Owned','',mode='fail')
    for _ in range(500):
        if b'incoming_message_failed' in (directory/'server.log').read_bytes():break
        time.sleep(.02)
    else:raise AssertionError('background parser failure not logged')
    configure('OwnedHostMessage','Owned','recovered ordinary');recovered=wait_event('recovered ordinary')
    observations.append(dict(case='background-failure-and-recovery',snapshot=recovered))
    value=request('POST','/api/plugins/OwnedHostMessage/clone',dict(suffix='copy'));assert value['data']['success'],value
    empty=snapshot('OwnedHostMessagecopy');assert empty['trace'] is None,empty
    configure('OwnedHostMessagecopy','OwnedClone','clone ordinary');clone=wait_event('clone ordinary','OwnedHostMessagecopy')
    assert clone['pid']!=recovered['pid'] and clone['config']['source']=='OwnedClone'
    assert [row['source'] for row in clone['trace'] if row['kind']=='parse']==['OwnedClone']
    source=snapshot();assert [row['source'] for row in source['trace'] if row['kind']=='parse']==['Owned']*4,source
    observations.append(dict(case='clone-namespace-and-parser-isolation',source=source,clone=clone))
    index['current_version']='2.0'
    value=request('POST','/api/plugins/install',dict(plugin_id='OwnedHostMessage',repo_url=repo_origin+'/index',release_version='2.0',force=True));assert value['data']['success'],value
    upgraded=snapshot();assert upgraded['version']=='2.0' and upgraded['pid']!=source['pid'] and upgraded['config']==source['config'] and upgraded['trace']==source['trace']
    clone_after=snapshot('OwnedHostMessagecopy');assert clone_after==clone
    stop();start();cold=snapshot();cold_clone=snapshot('OwnedHostMessagecopy')
    assert cold['pid']!=upgraded['pid'] and cold['trace']==upgraded['trace'] and cold['config']==upgraded['config']
    assert cold_clone['pid']!=clone['pid'] and cold_clone['trace']==clone['trace']
    observations.append(dict(case='actual-update-cold-no-replay',upgraded=upgraded,cold=cold,clone=cold_clone))
    if a.hold:
        (directory/'browser-state.json').write_text(json.dumps(dict(origin=origin,ready=True,plugin='OwnedHostMessage',source=cold['config'],clone=cold_clone['config']),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=origin)),flush=True)
        while not (directory/'.browser-done').exists():assert process.poll() is None;time.sleep(.2)
    for instance in ['OwnedHostMessagecopy','OwnedHostMessage']:
        value=request('POST','/api/plugins/'+instance+'/uninstall');assert value['data']['success'],value;request('GET','/api/v1/plugin/'+instance+'/snapshot',status=404)
    stop()
    bindings={name:hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest() for name,path in dict(server=a.server,worker=a.worker,worker_v2=a.worker_v2,oracle=a.oracle,driver=__file__).items()}
    (directory/'result.json').write_text(json.dumps(dict(status='passed',classification='actual '+platform.system()+' cmd/server + installed standard SDK; synthetic ordinary message payloads, no external channel service',bindings=bindings,observations=observations,uninstalled=True),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
    (directory/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+': '+str(error),observations=observations),ensure_ascii=False,indent=2)+'\n');raise
finally:
    stop();repo.shutdown();repo.server_close();log.close()
