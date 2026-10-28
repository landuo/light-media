"""Real local-class listing and idle browser check, no synthesized SDK output."""
import http.cookiejar,json,sys,time,urllib.request
from pathlib import Path
statefile=Path(sys.argv[1]);state=json.loads(statefile.read_text())
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,data=None,method='GET',headers=None,unwrap=True):
 req=urllib.request.Request(state['origin']+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Content-Type':'application/json',**(headers or {})})
 with op.open(req,timeout=30) as response:
  body=json.loads(response.read());return body['data'] if unwrap else body
request('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'},'POST')
key=request('/api/settings/api-token/reveal',{},'POST')['token']
local=request('/api/plugins?state=installed')
assert len(local)==1 and local[0]['instance_id']=='OwnedStandardFixture' and local[0]['version']=='1.0' and not local[0].get('repo_url') and not local[0].get('has_update') and local[0]['available']
def worker():
 body=request('/api/v1/plugin/OwnedStandardFixture/echo',headers={'X-API-KEY':key},unwrap=False)
 return {**{k:body[k] for k in ['version','nonce','init_count','config','value']},'get_state_calls':body['calls'].get('get_state',0)}
before=worker();fail=Path(str(statefile)+'.fail');fail.touch()
try:
 time.sleep(12)
 after=worker()
 assert before==after,'idle plugin page continued to poll class state or replaced the SDK'
finally:fail.unlink(missing_ok=True)
print(json.dumps({'kind':'real latest Linux cmd/server + actual browser idle12s + real owned Go SDK','source_local_query':'core/plugin.py:get_local_plugins and plugin.py:all_plugins(state=installed)','local_card':{k:local[0].get(k) for k in ['instance_id','version','repo_url','has_update','available']},'before_after_equal':True,'worker':before,'idle_seconds':12,'repository_failure_injected_during_idle':True},ensure_ascii=False))
