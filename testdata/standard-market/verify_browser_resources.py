"""Owned production resource publication/observations; never accesses user data."""
import http.cookiejar, json, os, pathlib, sys, time, urllib.request, urllib.error

scope = pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state = json.loads((scope/'browser-resources-state.json').read_text())
data = pathlib.Path(state['data_dir'])
source = pathlib.Path(state['resource_repository_root'])
versions = pathlib.Path(state['resource_versions_root'])
assert source == data/'owned-resource-publications'
assert versions == data/'owned-resource-versions'
phase = sys.argv[1]
if phase.startswith('publish-'):
    build = phase.removeprefix('publish-')
    corrupt = build == 'bad'
    if corrupt: build = 'three'
    assert build in ['one', 'two', 'three', 'backend2']
    index = (versions/build/'go-plugins.v1.json').read_bytes()
    declaration = json.loads(index)
    artifact = declaration['releases'][0]['artifacts'][0]
    name = artifact['url']
    assert pathlib.Path(name).name == name and declaration['plugin_id'] == 'OwnedFrontendFixture'
    pending = source/(name+'.pending')
    pending.write_bytes(b'owned incomplete browser resource build' if corrupt else (versions/build/name).read_bytes())
    os.replace(pending, source/name)
    pending = source/'go-plugins.v1.json.pending'
    pending.write_bytes(index)
    os.replace(pending, source/'go-plugins.v1.json')
    print(json.dumps(dict(kind='actual owned artifact publication', build=build, corrupt=corrupt)))
    sys.exit()

jar = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
client.open(urllib.request.Request(state['origin']+'/api/auth/login', data=json.dumps(dict(username='plugin-acceptance', password='owned-browser-test-password')).encode(), headers={'Content-Type':'application/json'})).read()
def get(path):
    return client.open(state['origin']+path).read()
def parsed(path): return json.loads(get(path))
if phase == 'cleanup':
    settings = parsed('/api/plugins/markets')['data']
    installed = parsed('/api/plugins?state=installed')['data']
    assert settings['plugin_local_repo_paths'] == [] and not settings['plugin_auto_reload']
    assert installed == []
    print(json.dumps(dict(kind='actual production cleanup after browser uninstall/save', installed_count=0, paths=[], auto_reload=False)))
    sys.exit()
def worker(identity):
    raw = parsed('/api/plugins/'+identity+'/api/status')
    return {key: raw[key] for key in ['nonce', 'init_count', 'cwd_ok', 'version', 'marker']}
def descriptor(identity): return parsed('/api/plugins/'+identity+'/frontend')['data']
record = data/'resource-browser-observations.json'
history = json.loads(record.read_text()) if record.exists() else {}
identity = 'OwnedFrontendFixture'
clone = identity+'resourcesui'
deadline = time.monotonic()+12
while True:
    try:
        primary, copy = worker(identity), worker(clone)
        page, copy_page = descriptor(identity), descriptor(clone)
        failures = sum(row.get('event')=='local_reload_failed' for row in parsed('/api/logs?module=plugins&limit=100')['data']['entries'])
        if phase in ['updated', 'recovered', 'backend']:
            previous = {'updated':'initial','recovered':'failed','backend':'recovered'}[phase]
            assert page['package_sha256'] != history[previous]['source_page']['package_sha256']
        if phase == 'failed': assert failures > history['updated']['failures']
        if phase == 'cold': assert primary['nonce'] != history['backend']['source']['nonce']
        if phase == 'backend': assert primary['version'] == '2.0'
        break
    except (urllib.error.HTTPError, AssertionError):
        if time.monotonic() >= deadline: raise
        time.sleep(.05)
assert primary['marker'] == 'resource-browser-source' and copy['marker'] == 'resource-browser-clone'
assert primary['cwd_ok'] and copy['cwd_ok'] and copy['version'] == '1.0'
assert primary['nonce'] != copy['nonce']
if phase in ['updated','failed','recovered']:
    assert primary == history['initial']['source'] and copy == history['initial']['clone']
    assert copy_page == history['initial']['clone_page']
if phase == 'failed': assert page == history['updated']['source_page']
if phase == 'backend':
    assert primary['nonce'] != history['initial']['source']['nonce'] and primary['init_count'] == 1
    assert copy == history['initial']['clone']
if phase == 'cold':
    assert primary['version'] == '2.0' and primary['init_count'] == copy['init_count'] == 1
    assert copy['nonce'] != history['initial']['clone']['nonce']
    assert page == history['backend']['source_page'] and copy_page == history['initial']['clone_page']
if phase == 'updated':
    try: get('/api'+history['initial']['source_page']['entry_path'])
    except urllib.error.HTTPError as error: assert error.code == 409
    else: raise AssertionError('old source resource URL remains authorized')
    assert b'Owned Vue two' in get('/api'+page['entry_path'].replace('remoteEntry.js', 'chunk.js'))
    assert b'Owned Vue one' in get('/api'+copy_page['entry_path'].replace('remoteEntry.js', 'chunk.js'))
observation = dict(kind='actual production HTTP after browser operations; owned actual SDK only', phase=phase, source=primary, clone=copy, source_page=page, clone_page=copy_page, failures=failures)
history[phase] = observation
record.write_text(json.dumps(history, indent=2))
print(json.dumps(observation))
