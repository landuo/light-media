import hashlib,json,pathlib,re,urllib.request,urllib.error
r=pathlib.Path.cwd();w=r/'work/deploy-main-20261029';origin='http://192.168.8.129:3120';expected=(r/'web/dist/index.html').read_text();assets=re.findall(r'(?:src|href)="([^"]+\.(?:js|css))"',expected);out=[]
for prefix in ['','/light-media']:
 with urllib.request.urlopen(origin+prefix+'/',timeout=10)as response:html=response.read().decode();assert response.status==200
 actual=re.findall(r'(?:src|href)="([^"]+\.(?:js|css))"',html)
 for asset in assets:
  relative=asset.lstrip('./');assert any(x.endswith(relative)for x in actual),(prefix,relative)
  url=origin+prefix+'/'+relative
  with urllib.request.urlopen(url,timeout=15)as response:body=response.read();assert response.status==200
  file=r/'web/dist'/relative;assert hashlib.sha256(body).digest()==hashlib.sha256(file.read_bytes()).digest()
 out.append({'prefix':prefix or'/','status':200,'exact_asset_hashes_verified':assets})
with urllib.request.urlopen(origin+'/api/auth/status',timeout=10)as response:auth=json.load(response)['data'];assert auth['setup_required']is False
try:urllib.request.urlopen(origin+'/api/plugins',timeout=10);raise AssertionError('anonymous plugins access accepted')
except urllib.error.HTTPError as e:assert e.code==401
report={'source_commit':'b1d9381ebaa41d03bd33d8af49bef968fa99025a','origin':origin,'pages':out,'existing_administrator_preserved':True,'anonymous_plugins_status':401};(w/'live-http-assets.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report))
