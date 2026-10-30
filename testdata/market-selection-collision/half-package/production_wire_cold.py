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
INPUT = HERE / 'mixed-entry-inputs.json'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
inputs, source = json.loads(INPUT.read_text()), json.loads(SOURCE.read_text())
assert source['input_sha256'] == sha(INPUT)
expected = {(s['name'],s['band']):s for s in source['observations']}
cold_source_path=SOURCE.with_name('cold-result.json')
cold_source=json.loads(cold_source_path.read_text())
cold_expected={(s['name'],s['band']):s for s in cold_source['observations']}
state={}
def response(host,path):
 assert host=='raw.githubusercontent.com'
 filename=path.split('?',1)[0].rsplit('/',1)[-1]
 if filename=='go-plugins.v1.json':return 404,'{}'
 assert filename in ('package.json','package.v2.json')
 return 200,state['body'] if filename==state['filename'] else '{}'

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

FIELDS={'name':'plugin_name','description':'plugin_desc','icon':'plugin_icon','version':'plugin_version','author':'plugin_author','history':'history'}
def project_source(rows):
 return [{'id':row['id'],'add_time':row['add_time'],**{key:row[value] for key,value in FIELDS.items()}} for row in rows]
def project_target(rows):
 return [{'id':row['instance_id'],'add_time':row['add_time'],**{key:row.get(key) for key in FIELDS}} for row in rows]
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
observations=[];pids=[]
try:
 for cold in [False,True]:
  pids.append(start())
  for example in inputs['cases']:
   for band,filename in [('base','package.json'),('higher','package.v2.json')]:
    repository='https://github.com/Owned/WireCold'+example['name'].replace('-','')+band
    assert call('PUT','/api/plugins/markets',{'plugin_markets':[repository],'plugin_local_repo_paths':[]})['data']['success'] is True
    wants=(cold_expected if cold else expected)[(example['name'],band)]['phases']
    for phase in range(2):
     force=not cold and phase==0
     package=example['package'] if force else {'OwnedCold':{'name':'Owned cold','version':'3.0','v2':True}}
     state.update(body=json.dumps(package),filename=filename)
     assert hashlib.sha256(state['body'].encode()).hexdigest()==wants[phase]['upstream_body_sha256']
     before=len(proxy.requests)
     raw=call('GET','/api/plugins/market?details=true&force='+str(force).lower())
     rows=raw['data']['items'];actual=project_target(rows);want=project_source(wants[phase]['body'])
     matched=actual==want
     observed={'name':example['name'],'band':band,'cold':cold,'phase':phase,'raw':raw,'actual':actual,'expected':want,'matches':matched,'requests':proxy.requests[before:],'upstream_body_sha256':hashlib.sha256(state['body'].encode()).hexdigest()}
     observations.append(observed)
     if not wants[phase]['requests']:assert not observed['requests'],'ordinary cache issued HTTP'
     assert all(row['runtime']=='python' and row['available'] is False and row['repository_version']==row['version'] for row in rows)
  stop()
 assert pids[0]!=pids[1]
 assert not proxy.rejected
 result={'server_sha256':sha(SERVER),'driver_sha256':sha(pathlib.Path(__file__)),'input_sha256':sha(INPUT),'warm_source_sha256':sha(SOURCE),'cold_source_sha256':sha(cold_source_path),'pids':pids,'observations':observations,'rejected':proxy.rejected,'boundary':__doc__,'isolated_settings':{'plugin_statistic_share':False,'plugin_server_url':''}}
 (OUT/'result.json').write_text(json.dumps(result,indent=2)+'\n')
 failures=[{'name':o['name'],'band':o['band'],'cold':o['cold'],'phase':o['phase']} for o in observations if not o['matches']]
 print(json.dumps({'phases':len(observations),'failures':failures,'same_runtime_cold':True}))
 assert not failures
finally:stop();log.close();proxy.close()
