"""Only owned UI3133 fixtures; actual platform clones, never user plugins."""
import http.cookiejar, json, pathlib, sys, urllib.request
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
s=json.loads((root/'browser-ui3133-platform2-state.json').read_text())
assert s['source_id']=='OwnedFrontendFixture' and '/TestPluginUI3133ProductionCLI' in s['data_dir']
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,value=None):
 req=urllib.request.Request(s['origin']+path,method=method,data=None if value is None else json.dumps(value).encode(),headers={'Content-Type':'application/json'})
 with client.open(req) as response:return json.load(response)
call('POST','/api/auth/login',{'username':'ui3133-acceptance','password':'owned-ui3133-password'})
created=[]
installed=call('GET','/api/plugins?state=installed')['data']
for suffix,name in [('quickone','Owned quick one'),('quicktwo','Owned quick two')]:
 identity=s['source_id']+suffix
 if sys.argv[1]=='create':
  previous=next((x for x in installed if x.get('instance_id')==identity),None)
  if previous:
   assert previous['name']==name,previous
  else:
   result=call('POST','/api/plugins/'+s['source_id']+'/clone',{'suffix':suffix,'name':name})
   assert result['data'].get('success') is True,result
  status=call('GET','/api/plugins/'+identity+'/api/status')
  created.append({'id':identity,'name':name,'sdk':status})
 elif sys.argv[1]=='cleanup':
  result=call('DELETE','/api/plugins/'+identity)
  assert result['data'].get('success') is True,result
  created.append({'id':identity,'uninstalled':True})
 else:raise SystemExit('create or cleanup')
print(json.dumps({'kind':'actual owned Linux platform clone '+sys.argv[1],'instances':created},ensure_ascii=False,indent=2))
