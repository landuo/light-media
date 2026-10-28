"""Owned center/browser evidence; never outputs the bootstrap API token or UID."""
import http.cookiejar,json,pathlib,sys,time,urllib.request

scope=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state=json.loads((scope/'browser-center-state.json').read_text())
assert state['origin'].startswith('http://127.0.0.1:') and state['plugin_center_origin'].startswith('http://127.0.0.1:')
phase=sys.argv[1]
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
client.open(urllib.request.Request(state['origin']+'/api/auth/login',data=json.dumps(dict(username='plugin-acceptance',password='owned-browser-test-password')).encode(),headers={'Content-Type':'application/json'})).read()
def get(path,headers=None):return json.loads(client.open(urllib.request.Request(state['origin']+path,headers=headers or {})).read())
def peer():return json.load(urllib.request.urlopen(state['plugin_center_origin']+'/owned/observations'))
settings=get('/api/plugins/markets')['data'];installed=get('/api/plugins?state=installed')['data']
record=pathlib.Path(state['data_dir'])/'owned-center-browser-observations.json'
history=json.loads(record.read_text()) if record.exists() else {}
if phase=='cleanup':
    assert installed==[] and settings['plugin_server_url']=='' and settings['plugin_statistic_share'] is True
    print(json.dumps(dict(kind='actual production cleanup after browser uninstall/settings save',installed_count=0,server_url='',share=True)))
    sys.exit()
assert settings['plugin_server_url']==state['plugin_center_origin']+'/prefix'
assert settings['plugin_statistic_share']==(phase!='disabled')
before=peer();statistics=get('/api/plugins/statistic')['data'];after=peer()
if phase in ['initial','installed','recovered','cold']:
    assert statistics['OwnedStandardFixture']==(1 if phase=='initial' else 2)
elif phase=='disabled':
    assert statistics=={} and before['requests']==after['requests']
elif phase=='failed':assert statistics=={} and after['failed']
else:raise ValueError('unknown owned browser phase')
assert after['uid_observed'] and after['bulk_attempts']==2 and after['bulk_successes']==1
observation=dict(kind='actual Linux host/browser with owned center HTTP contract; peer aggregation is synthetic',phase=phase,statistics=statistics,settings=dict(server_url=settings['plugin_server_url'],share=settings['plugin_statistic_share']),peer=after,installed_count=len(installed))
if phase!='initial':
    assert len(installed)==1
    token=json.loads(client.open(urllib.request.Request(state['origin']+'/api/settings/api-token/reveal',data=b'{}',headers={'Content-Type':'application/json'})).read())['data']['token']
    raw=get('/api/v1/plugin/OwnedStandardFixture/echo',{'X-API-KEY':token})
    worker={key:raw[key] for key in ['nonce','init_count','version','config','value']}
    assert worker['version']=='1.0' and worker['config']['key']=='center-source' and worker['value']=='center-source'
    observation['worker']=worker
    if phase in ['disabled','failed','recovered']:assert worker==history['installed']['worker']
    if phase=='cold':assert worker['nonce']!=history['installed']['worker']['nonce'] and worker['init_count']==1
history[phase]=observation;record.write_text(json.dumps(history,indent=2))
print(json.dumps(observation))
