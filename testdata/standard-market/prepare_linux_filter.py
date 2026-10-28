"""Prepare only the dedicated process-test market; no user sources or services."""
import hashlib,http.cookiejar,json,os,subprocess,sys,urllib.request
from pathlib import Path
state=json.loads(Path(sys.argv[1]).read_text()); fixture=Path(sys.argv[2]);root=Path(state['data_dir']).parent/'owned-market-local'
root.mkdir(mode=0o700);os.link(fixture,root/'worker')
manifest=json.loads(subprocess.check_output([str(fixture),'--manifest']));data=fixture.read_bytes();digest=hashlib.sha256(data).hexdigest()
index={'schema_version':1,'plugin_id':manifest['plugin_id'],'current_version':manifest['version'],'plugin_label':'owned-media,owned-local','releases':[{'version':manifest['version'],'manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','url':'worker','sha256':digest,'executable_sha256':digest,'size':len(data),'host_contract':'light-media.sdk-base/v1'}]}]}
(root/'go-plugins.v1.json').write_text(json.dumps(index))
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def req(path,data=None,method=None):
 r=urllib.request.Request(state['origin']+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Content-Type':'application/json'})
 with op.open(r,timeout=20) as response:return json.loads(response.read())['data']
req('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'})
req('/api/plugins/markets',{'plugin_markets':[state['repository']+'/index',state['repository']+'/market-metadata'],'plugin_local_repo_paths':[str(root)],'plugin_auto_reload':False,'plugin_server_url':state['repository'],'plugin_statistic_share':True},'PUT')
market=req('/api/plugins/market?details=true&force=true');stats=req('/api/plugins/statistic')
assert len(market['items'])==25,len(market['items'])
assert all(s['success'] for s in market['sources']),market['sources']
assert sum(c['available'] for c in market['items'])==2
assert sum(not c['available'] for c in market['items'])==23
assert len(stats)==25
print(json.dumps({'kind':'real cmd/server; actual Go standard/Base candidates plus23 explicit display-only owned Python metadata entries; no Python runtime','items':market['items'],'statistics':stats,'local_repository':str(root)},ensure_ascii=False))
