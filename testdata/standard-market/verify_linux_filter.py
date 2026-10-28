"""Read actual production HTTP market/settings after browser actions or restart."""
import http.cookiejar,json,sys,urllib.request
from pathlib import Path
state=json.loads(Path(sys.argv[1]).read_text())
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def req(path,data=None):
 r=urllib.request.Request(state['origin']+path,data=None if data is None else json.dumps(data).encode(),headers={'Content-Type':'application/json'})
 with op.open(r,timeout=20) as response:return json.loads(response.read())['data']
req('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'})
market=req('/api/plugins/market?details=true');stats=req('/api/plugins/statistic');settings=req('/api/plugins/markets');installed=req('/api/plugins/installed')
with urllib.request.urlopen(state['repository']+'/market-metadata',timeout=20) as response:source=json.load(response)
assert len(market['items'])==25 and all(s['success'] for s in market['sources'])
assert sum(c['available'] for c in market['items'])==2 and installed==[]
assert len(stats)==25
assert settings['plugin_markets']==[state['repository']+'/index',state['repository']+'/market-metadata']
assert len(settings['plugin_local_repo_paths'])==1 and settings['plugin_auto_reload']==False
print(json.dumps({'kind':'actual Linux cmd/server authenticated reads; original owned repository input captured separately; no synthetic successful SDK results','items':market['items'],'statistics':stats,'settings':settings,'installed':installed,'source_metadata':source},ensure_ascii=False))
