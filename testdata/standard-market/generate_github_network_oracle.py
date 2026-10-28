"""Pinned GitHub header/fallback AST with owned transport boundaries only."""
import ast,contextlib,hashlib,io,json,subprocess,sys
from pathlib import Path
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):return subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+path])
def extract(raw,names):
 methods=[n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name in names]
 for n in methods:n.decorator_list=[]
 return ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+methods,type_ignores=[])
cfg=source('app/core/config.py');ns={};exec(compile(ast.fix_missing_locations(extract(cfg,['GITHUB_HEADERS','REPO_GITHUB_HEADERS','NORMAL_USER_AGENT'])),'app/core/config.py','exec'),ns)
inputs=[('', '', ''),('owned-default','','owner/repo'),('owned-default','owner/repo:owned-repo,other/repo:owned-other','owner/repo'),('owned-default','owner/repo:owned-first,owner/repo:owned-last','owner/repo'),('owned-default',' owner/repo : owned-spaced ','owner/repo'),('owned-default','owner/repo:bad:discard,other/repo:owned-other','owner/repo'),('owned-default','owner/repo:owned-repo','Owner/repo'),('owned-default','owner/repo:owned-repo',''),('','owner/repo:owned-repo,','owner/repo')]
cases=[]
for token,pairs,repo in inputs:
 owner=NS(GITHUB_TOKEN=token,REPO_GITHUB_TOKEN=pairs);owner.NORMAL_USER_AGENT=ns['NORMAL_USER_AGENT'](owner);owner.GITHUB_HEADERS=ns['GITHUB_HEADERS'](owner)
 with contextlib.redirect_stdout(io.StringIO()):expected=ns['REPO_GITHUB_HEADERS'](owner,repo)
 cases.append({'token':token,'pairs':pairs,'repository':repo,'headers':expected})
plugin=source('app/helper/plugin.py');attempts=[];responses=[]
class Request:
 def __init__(self,**kwargs):self.kwargs=kwargs
 def get_res(self,**kwargs):
  attempts.append({'url':kwargs['url'],'explicit_proxy':bool(self.kwargs.get('proxies'))})
  if len(attempts)<responses[0]:raise OSError('owned failed request')
  return NS(ok=True)
url_source=source('app/utils/url.py');url_ns={};exec(compile(ast.fix_missing_locations(extract(url_source,['standardize_base_url'])),'app/utils/url.py','exec'),url_ns)
transport={"RequestUtils":Request,"UrlUtils":NS(standardize_base_url=url_ns['standardize_base_url']),"logger":NS(debug=lambda *a:None,error=lambda *a:None)}
exec(compile(ast.fix_missing_locations(extract(plugin,['__request_with_fallback'])),'app/helper/plugin.py','exec'),transport)
routes=[]
for mirror,proxy,api,success in [('https://owned-mirror.invalid/prefix','http://owned-proxy.invalid',False,1),('https://owned-mirror.invalid/prefix','http://owned-proxy.invalid',False,2),('https://owned-mirror.invalid/prefix','http://owned-proxy.invalid',False,3),('https://owned-mirror.invalid/prefix','http://owned-proxy.invalid',True,2),('https://owned-mirror.invalid/prefix','',False,2),('','',False,1),('owned-mirror.invalid/prefix','',False,1),('https://owned-mirror.invalid/prefix///','',False,1)]:
 attempts.clear();responses[:]=[success];transport['settings']=NS(GITHUB_PROXY=mirror,PROXY_HOST=proxy,PROXY={'http':proxy,'https':proxy} if proxy else None)
 address='https://api.github.com/repos/owner/repo/releases' if api else 'https://raw.githubusercontent.com/owner/repo/main/package.v2.json'
 transport['__request_with_fallback'](address,is_api=api)
 routes.append({'mirror':mirror,'proxy':proxy,'api':api,'success_on_attempt':success,'address':address,'attempts':list(attempts)})
Path(__file__).with_name('github-network-oracle.json').write_text(json.dumps({'source_commit':commit,'source_hashes':{'app/core/config.py':hashlib.sha256(cfg).hexdigest(),'app/helper/plugin.py':hashlib.sha256(plugin).hexdigest(),'app/utils/url.py':hashlib.sha256(url_source).hexdigest()},'kind':'actual pinned AST; owned tokens and fake request boundaries; not actual networking','headers':cases,'routes':routes},ensure_ascii=False,indent=2)+'\n')
