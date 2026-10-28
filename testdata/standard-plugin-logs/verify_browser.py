"""Read only the dedicated logs34 production test; no token is output."""
import http.cookiejar,json,pathlib,sys,urllib.request
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
s=json.loads((root/(sys.argv[2] if len(sys.argv)>2 else 'browser-logs34-state.json')).read_text());assert s['origin'].startswith('http://127.0.0.1:')
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
client.open(urllib.request.Request(s['origin']+'/api/auth/login',data=json.dumps({'username':'plugin-log-acceptance','password':'owned-log-browser-password'}).encode(),headers={'Content-Type':'application/json'})).read()
counts={}
for id in ['OwnedInstanceLog','OwnedInstanceLogtwin']:
 entries=json.load(client.open(s['origin']+'/api/plugins/'+id+'/logs'))['data']['history']['entries']
 assert all(e['plugin_instance_id']==id for e in entries)
 source=sum(e['message'].startswith('owned browser live') for e in entries)
 twin=sum(e['message']=='owned twin live private' for e in entries)
 assert (twin==0 if id=='OwnedInstanceLog' else source==0)
 counts[id]={'records':len(entries),'source_live_records':source,'twin_live_records':twin,'synchronous_failure_records':sum(e.get('event')=='notification_failed' for e in entries)}
print(json.dumps({'kind':'actual dedicated Linux history after browser operation','phase':sys.argv[1],'instances':counts}))
