"""Actual own SDK/source-clone observations around browser search selection."""
import http.cookiejar,json,sys,urllib.request
from pathlib import Path
state=json.loads(Path(sys.argv[1]).read_text());phase=sys.argv[2]
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def req(path,data=None,method=None,headers=None,unwrap=True):
 r=urllib.request.Request(state['origin']+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Content-Type':'application/json',**(headers or {})})
 with op.open(r,timeout=20) as response:
  v=json.load(response);return v['data'] if unwrap else v
req('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'})
key=req('/api/settings/api-token/reveal',{})['token']
source='OwnedStandardFixture';clone=source+'marketsearch'
def observe(instance):
 v=req('/api/v1/plugin/'+instance+'/echo',headers={'X-API-KEY':key},unwrap=False)
 return {k:v[k] for k in ['nonce','init_count','config','value','calls','version']}
if phase=='create':
 assert req('/api/plugins/installed')==[]
 assert req('/api/plugins/install',{'plugin_id':source,'repo_url':state['repository']+'/index'})['success']
 assert req('/api/plugins/'+source,{'enabled':True,'key':'owned-source-market-search'},'PUT')['success']
 assert req('/api/plugins/'+source+'/clone',{'suffix':'MarketSearch','name':'Owned market search clone','description':'Owned searchable clone','version':'9.7'})['success']
 assert req('/api/plugins/'+clone,{'enabled':True,'key':'owned-clone-market-search'},'PUT')['success']
 cards=req('/api/plugins?state=installed')
 card=next(c for c in cards if c['instance_id']==clone)
 assert card['display_version']=='9.7' and card['name']=='Owned market search clone'
 a,b=observe(source),observe(clone)
 assert a['nonce']!=b['nonce'] and a['value']!=b['value']
 print(json.dumps({'kind':'actual Linux cmd/server install/config/host clone; independent real SDK processes','source':a,'clone':b,'class':card},ensure_ascii=False))
else:
 before=json.loads(Path(sys.argv[3]).read_text());a,b=observe(source),observe(clone)
 for label,current in [('source',a),('clone',b)]:
  for field in ['nonce','init_count','config','value','version']:assert current[field]==before[label][field],(label,field)
 assert b['calls'].get('get_page',0)==before['clone']['calls'].get('get_page',0)+1
 assert a['calls'].get('get_page',0)==before['source']['calls'].get('get_page',0)
 print(json.dumps({'kind':'real browser-selected installed clone routed to own actual SDK get_page once; source/clone PID/config/KV unchanged','source':a,'clone':b},ensure_ascii=False))
 if phase=='cleanup':
  assert req('/api/plugins/'+clone,method='DELETE')['success']
  assert req('/api/plugins/'+source,method='DELETE')['success']
  assert req('/api/plugins/installed')==[]
