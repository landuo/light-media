"""Read-only observations of this task's actual owned Linux SDK/browser gate."""
import http.cookiejar,json,pathlib,sys,urllib.request
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
s=json.loads((root/'browser-render-state.json').read_text())
assert s['origin'].startswith('http://127.0.0.1:')
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
client.open(urllib.request.Request(s['origin']+'/api/auth/login',data=json.dumps({'username':'plugin-acceptance','password':'owned-browser-test-password'}).encode(),headers={'Content-Type':'application/json'})).read()
phase=sys.argv[1]
record=pathlib.Path(s['data_dir'])/'owned-render-browser-observations.json'
history=json.loads(record.read_text()) if record.exists() else {}
workers={}
for instance in ['OwnedRenderFixture','OwnedRenderFixtureview']:
 try:
  workers[instance]=json.load(client.open(s['origin']+'/api/plugins/'+instance+'/api/echo'))
 except urllib.error.HTTPError as error:
  if error.code!=404:raise
value={'kind':'actual production Linux browser/API and real SDK state readback','phase':phase,'workers':workers}
if phase=='cleanup':
 assert workers=={}, 'an uninstalled test plugin API is still active'
 history[phase]=value;record.write_text(json.dumps(history,ensure_ascii=False,indent=2));print(json.dumps(value,ensure_ascii=False));raise SystemExit
source=workers['OwnedRenderFixture']
if phase in ['saved','retry','clone','clone-page','source-page','cold']:
 expected='render-source' if phase=='saved' else 'render-retry'
 assert source['config']['key']==expected
 assert source['config']['enabled'] is False and source['config']['gate'] is False
 assert source['config']['notes']=='owned notes' and source['config']['total']==4
 assert source['config']['中文 标题']=='中文实际保存'
 assert source['config']['editor_content']=='owned: true\n'
 assert source['value']==('browser-page-write' if phase == 'source-page' else expected)
if phase=='saved':
 assert source['nonce']==history['initial']['workers']['OwnedRenderFixture']['nonce']
 assert source['init_count']==history['initial']['workers']['OwnedRenderFixture']['init_count']+1
if phase=='failed':
 before=history['saved']['workers']['OwnedRenderFixture']
 for field in ['nonce','init_count','config','value']:assert source[field]==before[field],field
if phase=='retry':
 before=history['failed']['workers']['OwnedRenderFixture']
 assert source['nonce']==before['nonce'] and source['init_count']==before['init_count']+1
if phase in ['clone','clone-page','source-page','cold']:
 clone=workers['OwnedRenderFixtureview']
 assert clone['nonce']!=source['nonce'] and clone['config']['key']=='render-clone'
 assert clone['value']==('render-clone' if phase in ['clone','cold'] else 'browser-page-write')
if phase=='cold':
 # Fixed core plugin.py:108 calls init on cold startup. This synthetic Go
 # fixture deliberately writes config.key into shared KV on EVERY init
 # (cmd/owned-standard-fixture/main.go:185), so its prior page write is
 # overwritten by its own documented side effect. This is not a persistence
 # failure or permission to change host behavior.
 for instance,worker in workers.items():
  assert worker['nonce']!=history['source-page']['workers'][instance]['nonce']
  assert worker['init_count']==1
history[phase]=value;record.write_text(json.dumps(history,ensure_ascii=False,indent=2));print(json.dumps(value,ensure_ascii=False))
