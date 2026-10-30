"""Complete fixed source package-cache boundaries through owned actual TLS.

The TTL witness changes only this test process's cache timer callback; original
cache algorithms, TTLs, size limits and helper/Manager methods are unchanged.
This is not a claim that thirty minutes of production wall time elapsed.
"""
import asyncio, contextlib, hashlib, io, json, os, pathlib, runpy, sys

HERE = pathlib.Path(__file__).resolve().parent
PARENT = HERE.parent
OUT = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_OUTPUT']).resolve()
sys.path.insert(0, str(PARENT))
n = runpy.run_path(str(PARENT / 'source_upstream.py'))
from app.helper.plugin import PluginHelper
from app.core.cache import MemoryBackend
from app.utils.http import aclose_shared_async_transports
from owned_http_mirror import OwnedHTTPMirror, assert_clean_environment
assert_clean_environment()

settings, api, root = n['settings'], n['api'], n['root']
inputs = json.loads((HERE / 'cache-edge-inputs.json').read_text())
state = {'bands': {}}
def response(host, path):
    assert host == 'raw.githubusercontent.com'
    parts = path.split('?', 1)[0].strip('/').split('/')
    assert len(parts) == 4 and parts[0] == 'Owned' and parts[2] == 'main'
    filename = parts[3]
    assert filename in ('package.json', 'package.v2.json')
    band = 'higher' if filename == 'package.v2.json' else 'base'
    selected = state['bands'][band]
    status = selected['status']
    if 'version' not in selected:
        return status, selected.get('body') or '{}'
    pid = 'OwnedHigher' if band == 'higher' else 'OwnedBase'
    return status, json.dumps({pid: {'name': pid, 'version': selected['version'], 'v2': True}})

proxy = OwnedHTTPMirror(response)
settings.update_setting('GITHUB_PROXY', proxy.url); assert not settings.PROXY_HOST
settings.update_setting('PLUGIN_LOCAL_REPO_PATHS', '')
observations, ttl, lru, expiry_lru = [], {}, {}, {}
def project(rows):
    return [{'id': row.id, 'version': row.plugin_version, 'repo': row.repo_url} for row in rows]
async def query(repository, bands, force):
    settings.update_setting('PLUGIN_MARKET', repository)
    state['bands'] = bands
    before = len(proxy.requests)
    rows = await api.all_plugins(None, state='market', force=force)
    return {'rows': project(rows), 'requests': proxy.requests[before:]}
async def execute():
    helper = PluginHelper()
    for sequence in inputs['sequences']:
        observed = {'name': sequence['name'], 'phases': []}
        for phase in sequence['phases']:
            value = await query(sequence['repository'], phase['bands'], phase['force'])
            observed['phases'].append({'name': phase['name'], 'force': phase['force'], **value})
        observations.append(observed)

    # The actual source cache is process-owned and cleared through its exposed
    # helper cache API. No source file/global clock/network policy is changed.
    await helper.async_get_plugins.cache_clear()
    repo = 'https://github.com/Owned/TTLClock'
    initial = {'higher': {'status': 200, 'version': '1.0'}, 'base': {'status': 200, 'version': '1.0'}}
    ttl['initial'] = await query(repo, initial, True)
    region = helper.async_get_plugins.cache_region
    matching = [value for key, value in MemoryBackend._region_caches.items() if key == region or key.endswith(':' + region)]
    assert len(matching) == 1, 'expected the actual helper cache region'
    cache = matching[0]
    timer = cache.timer
    original_clock = timer._Timer__timer
    clock = [original_clock()]
    start = clock[0]
    ttl['observed_configuration'] = {'maxsize': cache.maxsize, 'ttl': cache.ttl, 'type': type(cache).__name__}
    timer._Timer__timer = lambda: clock[0]
    try:
        clock[0] = start + 1000
        ttl['base_refreshed_at_1000'] = await query(repo, {'higher': {'status': 500}, 'base': {'status': 200, 'version': '2.0'}}, True)
        healthy = {'higher': {'status': 200, 'version': '3.0'}, 'base': {'status': 200, 'version': '3.0'}}
        clock[0] = start + 1799
        ttl['ordinary_at_1799'] = await query(repo, healthy, False)
        clock[0] = start + 1801
        ttl['ordinary_at_1801'] = await query(repo, healthy, False)
        clock[0] = start + 2801
        ttl['ordinary_at_2801'] = await query(repo, healthy, False)
    finally:
        timer._Timer__timer = original_clock

    await helper.async_get_plugins.cache_clear()
    state['bands'] = initial
    before = len(proxy.requests)
    for i in range(128):
        assert await helper.async_get_plugins('https://github.com/Owned/LRU' + str(i), None)
    lru['initial_fetches'] = len(proxy.requests) - before
    for label, number in [('touch_oldest', 0), ('insert_129th', 128), ('touched_survives', 0), ('next_oldest_refetched', 1)]:
        before = len(proxy.requests)
        result = await helper.async_get_plugins('https://github.com/Owned/LRU' + str(number), None)
        lru[label] = {'requests': proxy.requests[before:], 'ids': list(result)}
    await helper.async_get_plugins.cache_clear()
    await helper.async_get_plugins('https://github.com/Owned/ExpiryLRU0', None)
    matching = [value for key, value in MemoryBackend._region_caches.items() if key == region or key.endswith(':' + region)]
    assert len(matching) == 1
    timer = matching[0].timer
    original_clock = timer._Timer__timer
    clock = [original_clock()]
    start = clock[0]
    timer._Timer__timer = lambda: clock[0]
    try:
        clock[0] = start + 1000
        for i in range(1, 128):
            await helper.async_get_plugins('https://github.com/Owned/ExpiryLRU' + str(i), None)
        for label, number, elapsed in [('touch_expiring', 0, 1799), ('insert_after_expiry', 128, 1801), ('valid_oldest_survives', 1, 1801), ('expired_refetched', 0, 1801)]:
            clock[0] = start + elapsed
            before = len(proxy.requests)
            result = await helper.async_get_plugins('https://github.com/Owned/ExpiryLRU' + str(number), None)
            expiry_lru[label] = {'requests': proxy.requests[before:], 'ids': list(result)}
    finally:
        timer._Timer__timer = original_clock
    await aclose_shared_async_transports()

try:
    with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
        asyncio.run(execute())
    assert not proxy.rejected
    output = {'source_commit': n['inputs']['source_commit'], 'driver_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(), 'input_sha256': hashlib.sha256((HERE / 'cache-edge-inputs.json').read_bytes()).hexdigest(), 'base_source_result_sha256': hashlib.sha256((OUT / 'result.json').read_bytes()).hexdigest(), 'sequences': observations, 'ttl': ttl, 'lru': lru, 'expiry_lru': expiry_lru, 'rejected': proxy.rejected, 'boundary': __doc__}
    (OUT / 'cache-edge-result.json').write_text(json.dumps(output, indent=2) + '\n')
    print(json.dumps({'cache_sequences': len(observations), 'cache_phases': sum(len(x['phases']) for x in observations), 'ttl_configuration': ttl['observed_configuration'], 'lru_initial_fetches': lru['initial_fetches']}))
finally:
    proxy.close()
