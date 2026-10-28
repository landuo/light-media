"""Read-only observations of the owned services38 Linux/browser gate."""
import http.cookiejar,json,pathlib,sys,urllib.request,urllib.parse
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A');s=json.loads((root/'services38-browser-state.json').read_text());assert s['origin'].startswith('http://127.0.0.1:') and s['peer'].startswith('http://127.0.0.1:')
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def get(p):return json.load(client.open(s['origin']+p))
client.open(urllib.request.Request(s['origin']+'/api/auth/login',data=json.dumps({'username':'service-acceptance','password':'owned-service-browser-password'}).encode(),headers={'Content-Type':'application/json'})).read()
services=get('/api/plugins/services')['data'];counts=json.load(urllib.request.urlopen(s['peer']+'/owned/observations'))
progress={x['id']:get('/api/v1/dashboard/schedule/'+urllib.parse.quote(x['id'],safe='')+'/progress')['data'] for x in services}
record=pathlib.Path(s['data_dir'])/'owned-browser-services-evidence.json';history=json.loads(record.read_text()) if record.exists() else {}
value={'kind':'actual production Linux service/browser and real SDK side effect HTTP readback','phase':sys.argv[1],'services':services,'counts':counts,'progress':progress}
if sys.argv[1]=='running':
 assert counts.get('/manual',0)==history['initial']['counts'].get('/manual',0)+1, 'repeat submission caused more than one actual side effect'
 assert services[0]['status']=='正在运行' and services[0]['next_run'] is None
elif sys.argv[1]=='completed':
 assert all(x['status']!='正在运行' for x in services)
 assert counts.get('/manual',0)==history['running']['counts'].get('/manual',0)
elif sys.argv[1]=='cold':
 assert len(services)==len(history['completed']['services'])
 # Source recreates its in-memory jobs after startup. The existing Go
 # progress projection returns its waiting defaults rather than JSON null.
 for item in services:
  detail=item['progress_detail']
  assert detail['enable'] is False and detail['value']==0 and detail['data']=={}
  assert detail['started_at'] is None and detail['finished_at'] is None and detail['success'] is None
history[sys.argv[1]]=value;record.write_text(json.dumps(history,ensure_ascii=False,indent=2));print(json.dumps(value,ensure_ascii=False))
