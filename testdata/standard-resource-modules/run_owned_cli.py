"""Launch the isolated real cmd/server resource gate; development harness only."""
import argparse
import hashlib
import json
import os
import pathlib
import subprocess

p = argparse.ArgumentParser()
p.add_argument('--root', type=pathlib.Path, required=True)
p.add_argument('--server', required=True)
p.add_argument('--test', default='resources43-media-progress-final2-process-linux.test')
p.add_argument('--worker', default='resources43-media-progress-worker-linux')
p.add_argument('--prefix', required=True)
p.add_argument('--progress', action='store_true')
p.add_argument('--subscription-scope', action='store_true')
p.add_argument('--display-order', action='store_true')
a = p.parse_args()
b = a.root.resolve()
if pathlib.Path(a.prefix).name != a.prefix:
    p.error('prefix must be one owned evidence name')
e = os.environ.copy()
e.update(SEARCH_RESOURCE_PAGES='2', SEARCH_KEYWORD_MIN_INTERVAL='0', SEARCH_MULTIPLE_NAME='false', MAX_SEARCH_NAME_LIMIT='3')
for name, filename in {
    'PLUGIN_PROCESS_SERVER_BINARY': a.server,
    'OWNED_RESOURCE_MODULE_PROCESS_BINARY': a.worker,
    'PLUGIN_RESOURCE_SOURCE_ORACLE': 'resources43-domain-source-oracle.json',
    'PLUGIN_RESOURCE_STREAM_SOURCE_ORACLE': 'resources43-domain-stream-source-oracle.json',
    'PLUGIN_RESOURCE_BATCH_SOURCE_ORACLE': 'resources43-domain-batch-source-oracle.json',
    'PLUGIN_RESOURCE_FILTER_SOURCE_ORACLE': 'resources43-filter-source-oracle.json',
    'PLUGIN_RESOURCE_BYTES_SOURCE_ORACLE': 'bytes-source-oracle.json',
    'PLUGIN_MEDIA_FILTER_SOURCE_ORACLE': 'media-filter-source-oracle.json',
    'PLUGIN_RESOURCE_HTML_FIXTURE': 'ubits-search.sanitized.html',
}.items():
    e[name] = str(b / filename)
for flag in ['PLUGIN_RESOURCE_STREAM_GATE','PLUGIN_RESOURCE_BATCH_GATE','PLUGIN_RESOURCE_FILTER_GATE','PLUGIN_RESOURCE_BYTES_GATE','PLUGIN_MEDIA_RESOURCE_GATE']:
    e[flag] = 'true'
for flag, enabled in [('PLUGIN_MEDIA_PROGRESS_GATE',a.progress),('PLUGIN_MEDIA_SUBSCRIPTION_SCOPE_GATE',a.subscription_scope),('PLUGIN_RESOURCE_DISPLAY_ORDER_GATE',a.display_order)]:
    e[flag] = 'true' if enabled else 'false'
e['PLUGIN_RESOURCE_EVIDENCE_DIR'] = str(b / a.prefix)
log = b / (a.prefix + '.log')
with log.open('w') as f:
    result = subprocess.run([str(b/a.test), '-test.v', '-test.run', '^TestPluginSiteResourceModulesProductionCLI$', '-test.timeout', '90s'], cwd=b, env=e, stdout=f, stderr=subprocess.STDOUT)
summary = dict(exit_code=result.returncode, flags=dict(progress=a.progress,subscription_scope=a.subscription_scope,display_order=a.display_order), artifacts={key: hashlib.sha256((b/value).read_bytes()).hexdigest() for key,value in [('main',a.server),('test',a.test),('worker',a.worker)]}, log=str(log), log_sha256=hashlib.sha256(log.read_bytes()).hexdigest(), classification='actual separate production cmd/server + real owned SDK processes + controlled native HTTP/repository; not old service or real tracker/downloader credentials')
(b/(a.prefix+'-result.json')).write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary))
print('\n'.join(log.read_text().splitlines()[-8:]))
raise SystemExit(result.returncode)
