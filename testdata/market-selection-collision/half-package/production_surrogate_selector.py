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
INPUT = HERE / 'surrogate-selector-inputs.json'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
inputs, source = json.loads(INPUT.read_text()), json.loads(SOURCE.read_text())
assert source['input_sha256'] == sha(INPUT)
expected = {s['name']:s for s in source['observations']}
cold_source_path=SOURCE
cold_source=json.loads(cold_source_path.read_text())
cold_expected={s['name']:s for s in cold_source['observations']}
state={}
def response(host,path):
 assert host=='raw.githubusercontent.com'
 filename=path.split('?',1)[0].rsplit('/',1)[-1]
 if filename=='go-plugins.v1.json':return 404,'{}'
 assert filename in ('package.json','package.v2.json')
 return 200,json.dumps(state['packages']['higher' if filename=='package.v2.json' else 'base'])

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


import urllib.error
observations=[]
try:
 start()
 for example in inputs['cases']:
  state.update(example)
  repository='https://github.com/Owned/Key'+example['name'].replace('-','')
  assert call('PUT','/api/plugins/markets',{'plugin_markets':[repository],'plugin_local_repo_paths':[]})['data']['success'] is True
  for phase,force in enumerate([True,False]):
   before=len(proxy.requests)
   try:raw=call('GET','/api/plugins/market?details=true&force='+str(force).lower());status=200
   except urllib.error.HTTPError as e:status=e.code;raw=decode_response(e)
   want=expected[example['name']]['phases'][phase];matched=status==want['status'] and (status==200 or raw==want['body'])
   if status==200 and want['status']==200:
    actual=[{'id':r['instance_id'],'version':r['version']} for r in raw['data']['items']]
    expected_rows=[{'id':r['id'],'version':r['plugin_version']} for r in want['body']]
    matched=matched and actual==expected_rows
   requests=proxy.requests[before:]
   if not want['requests']:assert not requests
   observations.append({'name':example['name'],'force':force,'status':status,'raw':raw,'expected':want,'matches':matched,'requests':requests})
 assert not proxy.rejected
 output={'server_sha256':sha(SERVER),'driver_sha256':sha(pathlib.Path(__file__)),'input_sha256':sha(INPUT),'source_result_sha256':sha(SOURCE),'observations':observations,'rejected':proxy.rejected,'isolated_settings':{'plugin_statistic_share':False,'plugin_server_url':''}}
 (OUT/'result.json').write_text(json.dumps(output,indent=2)+'\n')
 print(json.dumps({'phases':len(observations),'statuses':[x['status'] for x in observations],'all_match':all(x['matches'] for x in observations)}));assert all(x['matches'] for x in observations)
finally:stop();log.close();proxy.close()
