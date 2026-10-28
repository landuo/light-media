"""Read-only evidence for the owned Linux plugin organization browser gate.
Only this disposable fixture is accessed; API token stays in memory.
"""
import http.cookiejar,json,pathlib,sys,urllib.request
scope=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state=json.loads((scope/'browser-organization-state.json').read_text())
assert state['origin'].startswith('http://127.0.0.1:')
phase=sys.argv[1]
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def request(path,body=None,headers=None):
 return json.load(client.open(urllib.request.Request(state['origin']+path,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json',**(headers or {})})))
request('/api/auth/login',dict(username='plugin-acceptance',password='owned-browser-test-password'))
installed=request('/api/plugins?state=installed')['data'];organization=request('/api/plugins/organization')['data']
observation={'kind':'actual production Linux browser/API/SDK readback; owned plugin','phase':phase,'organization':organization,'installed':[{'id':x['instance_id'],'name':x['name'],'state':x['state'],'has_update':x.get('has_update',False)} for x in installed]}
record=pathlib.Path(state['data_dir'])/'owned-organization-browser-evidence.json'
history=json.loads(record.read_text()) if record.exists() else {}
if phase=='cleanup':
 assert installed==[] and organization['folders']=={}
else:
 assert {x['instance_id'] for x in installed}=={'OwnedStandardFixture','OwnedStandardFixtureorg'}
 token=request('/api/settings/api-token/reveal',{})['data']['token']
 workers={}
 for id in ['OwnedStandardFixture','OwnedStandardFixtureorg']:
  value=request('/api/v1/plugin/'+id+'/echo?key=shared',headers={'X-API-KEY':token})
  workers[id]={k:value[k] for k in ['nonce','init_count','version','config','value']}
  assert workers[id]['nonce']>0 and workers[id]['init_count']>=1
 assert workers['OwnedStandardFixture']['nonce']!=workers['OwnedStandardFixtureorg']['nonce']
 assert workers['OwnedStandardFixture']['config']['key']=='organization-source'
 assert workers['OwnedStandardFixtureorg']['config']['key']=='organization-clone'
 observation['workers']=workers
 if phase in ['grouped','sorted','appearance','failed','recovered']:
  assert workers==history['installed']['workers'], 'group operation changed SDK state'
 if phase=='grouped':
  assert organization['folders']['归档组']['plugins']==['OwnedStandardFixtureorg']
 if phase=='sorted':
  assert organization['folders']['归档组']['plugins']==['OwnedStandardFixture','OwnedStandardFixtureorg']
 if phase=='appearance':
  assert organization['folders']['媒体组']['showIcon'] is False
 if phase=='failed':
  assert '故障组' not in organization['folders']
 if phase=='recovered':assert organization['folders']['故障组']['plugins']==[]
 if phase=='cold':
  for id in workers:
   assert workers[id]['nonce']!=history['installed']['workers'][id]['nonce']
   for k in ['config','value']:assert workers[id][k]==history['installed']['workers'][id][k]
   assert workers[id]['version']==('2.0' if id=='OwnedStandardFixture' else '1.0'), 'actual promoted source startup sync updates only source code'
  assert organization==history['recovered']['organization']
 if phase=='ungrouped':
  assert organization['folders']=={}
  assert workers==history['cold']['workers']
history[phase]=observation;record.write_text(json.dumps(history,ensure_ascii=False,indent=2))
print(json.dumps(observation,ensure_ascii=False))
