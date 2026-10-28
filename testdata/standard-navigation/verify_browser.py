"""Read-only native state observations for the owned production UI3133 gate."""
import http.cookiejar,json,pathlib,sys,urllib.request
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A');s=json.loads((root/(sys.argv[2] if len(sys.argv)>2 else 'browser-ui3133-final-state.json')).read_text());assert s['origin'].startswith('http://127.0.0.1:')
c=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
c.open(urllib.request.Request(s['origin']+'/api/auth/login',data=json.dumps({'username':'ui3133-acceptance','password':'owned-ui3133-password'}).encode(),headers={'Content-Type':'application/json'})).read()
def get(p):return json.load(c.open(s['origin']+p))['data']
x={'phase':sys.argv[1],'kind':'actual own Linux state after browser operations','navigation':get('/api/plugins/navigation'),'sidebar':get('/api/plugins/sidebar_nav'),'meta':get('/api/plugins/dashboard/meta'),'settings':{k:get('/api/plugins/dashboard/settings/'+k) for k in ['enabled','order','desktop','tablet','mobile']}}
x['workers']={id:json.load(c.open(s['origin']+'/api/plugins/'+id+'/api/status')) for id in [s['source_id'],s['clone_id']]}
if sys.argv[1]=='saved':
 assert x['workers'][s['source_id']]['marker']=='browser-source'
 assert x['workers'][s['source_id']]['init_count']==2
 assert x['workers'][s['clone_id']]['marker']=='clone'
 assert x['workers'][s['clone_id']]['init_count']==1
 assert len(x['navigation'])==4 and len(x['meta'])==4
 selected=x['settings']['enabled']['value']
 assert selected[s['source_id']+':details'] and selected[s['source_id']+':summary']
 assert not selected[s['clone_id']+':details']
print(json.dumps(x,ensure_ascii=False))
