"""Real authenticated market HTTP + normal product mirror-prefix HTTP, source-derived cache edges.

This test does not install workers; worker installation/cold recovery is covered
by the separate production_upstream runner. Private runtime data stay local.
"""
import hashlib, http.cookiejar, json, os, pathlib, secrets, signal, socket, subprocess, sys, time, urllib.request

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
from owned_http_mirror import OwnedHTTPMirror, assert_clean_environment
assert_clean_environment()
OUT = pathlib.Path(os.environ['MARKET_SELECTION_OUTPUT']).resolve(); OUT.mkdir()
SERVER = pathlib.Path(os.environ['MARKET_SELECTION_SERVER']).resolve()
SOURCE = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_RESULT']).resolve()
INPUT = HERE / 'history-wire-inputs.json'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
inputs, source = json.loads(INPUT.read_text()), json.loads(SOURCE.read_text())
assert source['input_sha256'] == sha(INPUT)
expected = {s['name']:s for s in source['observations']}
cold_source_path=SOURCE.with_name('cold-result.json')
cold_source=json.loads(cold_source_path.read_text())
cold_expected={s['name']:s for s in cold_source['observations']}
state={}
def response(host,path):
 assert host=='raw.githubusercontent.com'
 filename=path.split('?',1)[0].rsplit('/',1)[-1]
 if filename=='go-plugins.v1.json':return 404,'{}'
 assert filename in ('package.json','package.v2.json')
 return 200,state['body'] if filename=='package.v2.json' else '{}'

proxy = OwnedHTTPMirror(response)
home, runtime = OUT / 'home', OUT / 'runtime'; home.mkdir(); runtime.mkdir()
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
origin = 'http://127.0.0.1:' + str(port)
username, password = 'owned-cache-edges', secrets.token_urlsafe(24)
env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'LANG': 'C.UTF-8', 'TZ': 'UTC', 'DEV': 'false', 'LIGHT_MEDIA_SAFE_MODE': 'false', 'MOVIEPILOT_DOCKER': 'false'}
bootstrap = {'username': username, 'password': password, 'settings': {'api_token': secrets.token_hex(32), 'github_proxy': proxy.url, 'plugin_markets': [], 'plugin_statistic_share': False, 'plugin_server_url': ''}}
setup = subprocess.run([str(SERVER), '--data-dir', str(runtime), '--bootstrap-stdin'], input=json.dumps(bootstrap).encode(), env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=30)
assert setup.returncode == 0
cookie = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookie))
def call(method, path, body=None):
    request = urllib.request.Request(origin + path, data=None if body is None else json.dumps(body).encode(), method=method, headers={'Content-Type': 'application/json'})
    with opener.open(request, timeout=45) as response:
        return json.load(response)


def decode_response(response):
    raw = response.read()
    try: return json.loads(raw)
    except ValueError: return raw.decode('utf-8')

process=None
log=(OUT/'server-runtime.log').open('wb')
def start():
 global process
 process=subprocess.Popen([str(SERVER),'--data-dir',str(runtime),'--listen','127.0.0.1:'+str(port)],env=env,stdout=log,stderr=log)
 for _ in range(400):
  try:call('GET','/api/auth/status');break
  except Exception:
   assert process.poll() is None,'owned server exited';time.sleep(.025)
 else:raise AssertionError('owned server did not become ready')
 call('POST','/api/auth/login',{'username':username,'password':password})
 configured=call('GET','/api/plugins/markets')['data']
 assert configured['plugin_statistic_share'] is False and configured['plugin_server_url']==''
 return process.pid
def stop():
 global process
 if process is not None and process.poll() is None:process.send_signal(signal.SIGTERM);process.wait(timeout=35)
 process=None

import shutil,urllib.parse,urllib.error
worker=pathlib.Path(os.environ['MARKET_SELECTION_NATIVE_WORKER']).resolve()
manifest=json.loads(subprocess.check_output([str(worker),'--manifest']))
assert manifest['plugin_id']=='OwnedA1' and manifest['version']=='2.0'
local=OUT/'owned-local';local.mkdir();shutil.copyfile(worker,local/'OwnedA1')
index={'schema_version':1,'plugin_id':'OwnedA1','current_version':'2.0','releases':[{'version':'2.0','manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','url':'OwnedA1','sha256':sha(worker),'executable_sha256':sha(worker),'size':worker.stat().st_size,'host_contract':'light-media.sdk-base/v1'}]}]}
(local/'go-plugins.v1.json').write_text(json.dumps(index))
observations=[];pids=[];workers=[]
def observe(path):
 try:return 200,call('GET',path)
 except urllib.error.HTTPError as e:return e.code,decode_response(e)
try:
 for cold in [False,True]:
  pids.append(start())
  if not cold:
   call('PUT','/api/plugins/markets',{'plugin_markets':[],'plugin_local_repo_paths':[str(local)]})
   repo='local://OwnedA1?'+urllib.parse.urlencode({'path':str(local)})
   assert call('POST','/api/plugins/install',{'plugin_id':'OwnedA1','repo_url':repo,'release_version':'2.0','force':True})['data']['success'] is True
  echo=call('GET','/api/v1/plugin/OwnedA1/echo?apikey='+bootstrap['settings']['api_token'])
  assert echo['init_count']==1 and echo['version']=='2.0';workers.append(echo['nonce'])
  for example in inputs['cases']:
   repository='https://github.com/Owned/History'+example['name'].replace('-','')
   assert call('PUT','/api/plugins/markets',{'plugin_markets':[repository],'plugin_local_repo_paths':[]})['data']['success'] is True
   wants=(cold_expected if cold else expected)[example['name']]['phases']
   for phase in range(2):
    force=not cold and phase==0
    state['body']=example['package_json'] if force else '{"OwnedA1":{"name":"Owned changed","version":"4.0","history":["after cold"]}}'
    for route in ['history','releases']:
     want=next(p for p in wants if p['phase']==phase and p['route']==route)
     assert hashlib.sha256(state['body'].encode()).hexdigest()==want['upstream_body_sha256']
     before=len(proxy.requests)
     query={'force':str(force).lower()}
     if route=='releases':query['repo_url']=repository
     status,raw=observe('/api/plugins/'+route+'/OwnedA1?'+urllib.parse.urlencode(query))
     matched=status==want['status'] and (status==200 or raw==want['body'])
     if status==200 and want['status']==200:
      data=raw['data'];expected_value=want['body']
      if route=='history':matched=matched and data['history']==expected_value['history'] and data['current_version']==expected_value['plugin_version']
      else:matched=matched and data['latest_version']==expected_value['latest_version'] and data['current_version']==expected_value['current_version'] and data['items']==expected_value['items'] and data['release_supported']==expected_value['release_supported']
     requests=proxy.requests[before:]
     if not want['requests']:assert not requests,'source ordinary cache hit issued HTTP'
     observations.append({'name':example['name'],'cold':cold,'phase':phase,'route':route,'status':status,'raw':raw,'expected':want,'matches':matched,'requests':requests})
  stop();cookie.clear()
 assert pids[0]!=pids[1] and workers[0]!=workers[1]
 assert not proxy.rejected
 result={'server_sha256':sha(SERVER),'worker_sha256':sha(worker),'driver_sha256':sha(pathlib.Path(__file__)),'input_sha256':sha(INPUT),'warm_source_sha256':sha(SOURCE),'cold_source_sha256':sha(cold_source_path),'pids':pids,'worker_pids':workers,'observations':observations,'rejected':proxy.rejected,'isolated_settings':{'plugin_statistic_share':False,'plugin_server_url':''},'boundary':'Actual production authenticated history/releases HTTP + normal installed standard Go worker, same persisted data after cold start; source whole component/ASGI separately bound. Native extra history/latest_version fields are not source route fields.'}
 (OUT/'result.json').write_text(json.dumps(result,indent=2)+'\n')
 failures=[{k:o[k] for k in ['name','cold','phase','route','status']} for o in observations if not o['matches']]
 print(json.dumps({'phases':len(observations),'failures':failures,'same_runtime_cold':True,'actual_worker_restarted':workers[0]!=workers[1]}));assert not failures
finally:stop();log.close();proxy.close()
