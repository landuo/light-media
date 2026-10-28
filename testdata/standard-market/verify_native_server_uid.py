"""Compare actual pinned root UID with owned peer in memory; disclose neither."""
import hashlib,json,os,pathlib,platform,urllib.request,uuid
from types import SimpleNamespace as NS
scope=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state=json.loads((scope/'browser-center-state.json').read_text())
oracle=json.loads((scope/'server-uid-oracle.json').read_text())
assert oracle['source_commit']=='8a83efeacd3e26b82be495e17d50146e31c96a60'
ns=dict(hashlib=hashlib,os=os,uuid=uuid,print=lambda *args:None)
exec(compile(oracle['method_source'],'fixed/SystemUtils.generate_user_unique_id','exec'),ns)
uid=ns['generate_user_unique_id']()
assert uid and len(uid)==64
ns.update(platform=platform,APP_VERSION=oracle['compatibility_version'])
exec(compile(oracle['cpu_methods_source'],'fixed/SystemUtils.cpu_arch','exec'),ns)
exec(compile(oracle['user_agent_method_source'],'fixed/settings.USER_AGENT','exec'),ns)
agent=ns['USER_AGENT'](NS(PROJECT_NAME=oracle['project_name']))
def unavailable_root(path):raise OSError('owned forced root failure')
ns['os']=NS(stat=unavailable_root)
mac_uid=ns['generate_user_unique_id']() or ''
request=urllib.request.Request(state['plugin_center_origin']+'/owned/compare-uid',headers={'X-Owned-Expected-UID':uid,'X-Owned-Expected-UA':agent,'X-Owned-Expected-MAC-UID':mac_uid})
result=json.load(urllib.request.urlopen(request))
assert result['observed'] and result['matches_fixed_native_source'] and result['matches_fixed_native_user_agent'] and result['matches_fixed_native_mac_fallback']
print(json.dumps(dict(kind='actual native fixed source vs actual Linux Go process HTTP identity headers; actual OS MAC adapter with forced root failure; own peer only',matches_fixed_native_source=True,matches_fixed_native_user_agent=True,matches_fixed_native_mac_fallback=True,mac_identity_available=result['mac_identity_available'],identity_values_withheld=True)))
