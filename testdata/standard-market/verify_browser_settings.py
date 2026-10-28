"""Read only the isolated process fixture's settings after browser operations.
Synthetic local login; does not fetch a real token or any user service.
"""
import http.cookiejar,json,pathlib,sys,urllib.request
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state=json.loads((root/'browser-github-settings-state.json').read_text())
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
request=urllib.request.Request(state['origin']+'/api/auth/login',data=json.dumps(dict(username='plugin-acceptance',password='owned-browser-test-password')).encode(),headers={'Content-Type':'application/json'})
client.open(request).read()
raw=client.open(state['origin']+'/api/plugins/markets').read();settings=json.loads(raw)['data']
assert not settings['github_token_set'] and not settings['repo_github_token_set']
assert settings['github_proxy']=='' and not settings['plugin_auto_reload'] and settings['plugin_local_repo_paths']==[]
for sentinel in [b'owned-browser-github-sentinel',b'owned-browser-repository-sentinel']:
 assert sentinel not in raw
installed=json.loads(client.open(state['origin']+'/api/plugins?state=installed').read())['data']
assert not installed
restart=json.loads((root/'browser-github-settings-state.json.restart.done').read_text())
assert restart['previous_pid']!=restart['current_pid']
print(json.dumps(dict(kind='actual browser changes cross-checked by real production HTTP; owned process only',github_token_cleared=True,repository_token_cleared=True,mirror_cleared=True,local_paths_cleared=True,auto_reload_disabled=True,ordinary_queries_omit_sentinels=True,installed_plugins=0,restored_repository_count=len(settings['plugin_markets']),restart=restart),indent=2))
