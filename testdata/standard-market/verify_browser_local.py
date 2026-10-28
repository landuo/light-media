"""Observe genuine SDK processes/config/KV after this owned browser workflow."""
import http.cookiejar,json,pathlib,sys,time,urllib.request,urllib.error
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A');state=json.loads((root/'browser-local-dev-state.json').read_text());phase=sys.argv[1]
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));client.open(urllib.request.Request(state['origin']+'/api/auth/login',data=json.dumps(dict(username='plugin-acceptance',password='owned-browser-test-password')).encode(),headers={'Content-Type':'application/json'})).read()
# The source plugin API requires the application's API token, independently
# of the browser admin session. Reveal only the owned bootstrap token into
# memory and send it only to this exact loopback process; never print/store it.
api_token=json.loads(client.open(urllib.request.Request(state['origin']+'/api/settings/api-token/reveal',data=b'{}',headers={'Content-Type':'application/json'})).read())['data']['token']
def get(path):
 headers={'X-API-KEY':api_token} if path.startswith('/api/v1/plugin/') else {}
 return json.loads(client.open(urllib.request.Request(state['origin']+path,headers=headers)).read())
def worker(identity):
 raw=get('/api/v1/plugin/'+identity+'/echo');return {key:raw[key] for key in ['version','nonce','init_count','config','value','calls']}
def failures():return sum(row.get('event')=='local_reload_failed' for row in get('/api/logs?module=plugins&limit=100')['data']['entries'])
path=pathlib.Path(state['data_dir'])/'owned-browser-local-observations.json';history=json.loads(path.read_text()) if path.exists() else {}
deadline=time.monotonic()+10
while True:
 try:
  source=worker('OwnedStandardFixture');clone=worker('OwnedStandardFixturelocalui');count=failures()
  if phase in ['updated','recovered'] and source['version']!='2.0':raise AssertionError('waiting for actual v2 SDK')
  if phase=='recovered' and source['nonce']==history['failed']['source']['nonce']:raise AssertionError('waiting for actual valid recovery reload')
  if phase=='failed' and count<=history['updated']['local_reload_failure_count']:raise AssertionError('waiting for actual reload failure')
  break
 except (urllib.error.HTTPError,AssertionError):
  if time.monotonic()>deadline:raise
  time.sleep(.05)
assert source['config']['key']=='browser-local-source' and source['value']=='browser-local-source'
assert clone['config']['key']=='browser-local-clone' and clone['value']=='browser-local-clone'
assert source['nonce']!=clone['nonce'] and clone['version']=='1.0'
if phase=='initial':assert source['version']=='1.0'
elif phase=='updated':
 assert source['nonce']!=history['initial']['source']['nonce'] and source['init_count']==1
 assert clone['nonce']==history['initial']['clone']['nonce']
elif phase=='failed':
 assert source['version']=='2.0' and source['nonce']==history['updated']['source']['nonce']
 assert clone['nonce']==history['updated']['clone']['nonce']
elif phase=='recovered':
 assert source['nonce']!=history['failed']['source']['nonce'] and clone['nonce']==history['failed']['clone']['nonce']
elif phase=='cold':
 assert source['version']=='2.0' and source['nonce']!=history.get('recovered',history['failed'])['source']['nonce']
 assert clone['nonce']!=history['failed']['clone']['nonce']
 assert source['init_count']==clone['init_count']==1
elif phase in ['duplicates','future']:
 assert source['version']=='2.0' and source['nonce']==history['cold']['source']['nonce']
 assert clone['nonce']==history['cold']['clone']['nonce']
elif phase=='disabled':
 assert source['version']=='2.0' and source['nonce']==history['cold']['source']['nonce']
 assert clone['nonce']==history['cold']['clone']['nonce']
else:raise ValueError('unknown owned phase')
settings=get('/api/plugins/markets')['data']
expected_roots=[state['local_repository_root']]
if phase=='duplicates':expected_roots*=2
if phase=='future':expected_roots=[str(pathlib.Path(state['local_repository_root'])/'future-not-built')]
assert settings['plugin_local_repo_paths']==expected_roots
if phase=='duplicates':
 source_before,clone_before=source,clone
 market=get('/api/plugins/market?details=true')['data']
 source,clone=worker('OwnedStandardFixture'),worker('OwnedStandardFixturelocalui')
 assert len(market['sources'])==2
 assert source['calls']['get_state']==source_before['calls']['get_state']+2
 assert clone['calls']['get_state']==clone_before['calls']['get_state']+1
 assert source['nonce']==source_before['nonce'] and source['init_count']==source_before['init_count']
if phase=='future':
 market=get('/api/plugins/market?details=true')['data']
 assert len(market['sources'])==1 and not market['sources'][0]['success'] and market['sources'][0]['message']
 assert market['items']==[]
assert settings['plugin_auto_reload']==(phase!='disabled')
observation=dict(kind='real production HTTP genuine SDK after browser steps; owned local peers only',phase=phase,source=source,clone=clone,local_reload_failure_count=count,local_path=state['local_repository_root'],auto_reload=settings['plugin_auto_reload'])
observation['configured_paths']=settings['plugin_local_repo_paths']
if phase=='duplicates':observation['merged_getter_delta']={'source':source['calls']['get_state']-source_before['calls']['get_state'],'clone':clone['calls']['get_state']-clone_before['calls']['get_state']}
history[phase]=observation;path.write_text(json.dumps(history));print(json.dumps(observation,indent=2))
