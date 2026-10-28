"""Actual Linux update of the exact-ID clone created in the browser.

Expected class-attribute replacement is from fixed PluginHelper.install and
PluginManager.clone_plugin; this is an owned fixture, not a business plugin.
"""
import http.cookiejar, json, sys, urllib.request
from pathlib import Path
state=json.loads(Path(sys.argv[1]).read_text())
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,data=None,method='GET',headers=None,unwrap=True):
 req=urllib.request.Request(state['origin']+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Content-Type':'application/json',**(headers or {})})
 with opener.open(req,timeout=30) as response:
  body=json.loads(response.read())
  return body['data'] if unwrap else body
request('/api/auth/login',{'username':'plugin-acceptance','password':'owned-browser-test-password'},'POST')
key=request('/api/settings/api-token/reveal',{},'POST')['token']
source='OwnedStandardFixture';clone=source+'exact'
def worker(id):
 body=request('/api/v1/plugin/'+id+'/echo',headers={'X-API-KEY':key},unwrap=False)
 return {k:body[k] for k in ['version','nonce','init_count','config','value']}
def card(id):
 body=next(c for c in request('/api/plugins') if c['instance_id']==id)
 return {k:body.get(k) for k in ['instance_id','source_instance_id','name','description','display_version','has_update']}
before=worker(clone);source_before=worker(source);card_before=card(clone)
assert card_before['display_version']=='9.7' and not card_before['has_update']
history=request('/api/plugins/history/'+clone)
assert history['current_version']=='9.7' and history['latest_version']=='2.0'
result=request('/api/plugins/install',{'plugin_id':clone,'repo_url':state['repository']+'/index','force':True},'POST')
assert result['success'] is True
after=worker(clone);source_after=worker(source);card_after=card(clone)
assert after['version']=='2.0' and after['init_count']==1 and after['nonce']!=before['nonce']
assert after['config']==before['config'] and after['value']==before['value'] and source_after==source_before
assert card_after['name']=='Owned exact class' and card_after['description'] in (None,'') and card_after['display_version']=='2.0' and card_after['source_instance_id']==source and not card_after['has_update']
assert request('/api/plugins/history/'+clone)['current_version']=='2.0'
print(json.dumps({'kind':'real latest Linux cmd/server, browser-created clone, actual separate Go artifacts','before_worker':before,'after_worker':after,'before_class':card_before,'after_class':card_after,'source_worker_unchanged':True},ensure_ascii=False))
