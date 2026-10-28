"""Observe actual browser status actions via real SDK KV; never synthesize events."""
import http.cookiejar,json,sys,urllib.request
from pathlib import Path
state=json.loads(Path(sys.argv[1]).read_text()); expected=sys.argv[2]
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def req(path,data=None,headers=None,unwrap=True):
 r=urllib.request.Request(state['origin']+path,data=None if data is None else json.dumps(data).encode(),headers={'Content-Type':'application/json',**(headers or {})})
 with op.open(r,timeout=20) as response:
  v=json.loads(response.read());return v['data'] if unwrap else v
req('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'})
key=req('/api/settings/api-token/reveal',{})['token']
rows=req('/api/subscriptions')['items']; sub=next(s for s in rows if s['id']=='sub_owned_plugin_events_browser')
assert sub['state']==expected,(sub['state'],expected)
observations=[]
for instance in ['OwnedEventFixture','OwnedEventFixturesubscription']:
 raw=req('/api/v1/plugin/'+instance+'/events',headers={'X-API-KEY':key},unwrap=False)
 events=[e['data'] for e in raw['events'] if e['type']=='subscribe.modified' and e['data']['subscribe_id']==sub['id']]
 assert len(events)==(1 if expected=='active' else 2),len(events)
 event=events[-1];assert event['scene']=='status' and event['fields']==['state'],event['fields']
 assert event['subscribe_info']['state']==('R' if expected=='active' else 'S')
 assert event['subscribe_info']['total_episode']==2
 observations.append({'instance':instance,'pid':raw['nonce'],'browser_status_events':len(events),'last_event':event})
assert observations[0]['pid']!=observations[1]['pid']
assert observations[0]['last_event']==observations[1]['last_event']
print(json.dumps({'kind':'real Linux production server + browser pause/resume + independent actual Go SDK KV','native_state':sub['state'],'revision':sub['revision'],'observations':observations},ensure_ascii=False))
