import hashlib
import base64
import io
import json
import pathlib
import tarfile
import urllib.request
import urllib.error

ROOT=pathlib.Path(__file__).resolve().parent
metadata=json.load(urllib.request.urlopen('https://pypi.org/pypi/Pinyin2Hanzi/0.1.1/json',timeout=30))
artifact=next(a for a in metadata['urls'] if a['packagetype']=='sdist')
raw=urllib.request.urlopen(artifact['url'],timeout=30).read()
assert hashlib.sha256(raw).hexdigest()==artifact['digests']['sha256']
licenses={}
with tarfile.open(fileobj=io.BytesIO(raw),mode='r:*') as archive:
    for entry in archive.getmembers():
        if entry.isfile() and any(x in entry.name.lower() for x in ['license','copying']):
            licenses[entry.name]=archive.extractfile(entry).read().decode()
upstream=None
declaration_only=False
if not licenses:
    request=urllib.request.Request('https://api.github.com/repos/someus/Pinyin2Hanzi/license',headers={'Accept':'application/vnd.github+json','User-Agent':'source-license-reference'})
    try:
        upstream=json.load(urllib.request.urlopen(request,timeout=30))
        assert upstream['license']['spdx_id']=='MIT'
    except urllib.error.HTTPError as err:
        if err.code!=404:raise
        request=urllib.request.Request('https://api.github.com/repos/letiantian/Pinyin2Hanzi/readme',headers={'Accept':'application/vnd.github+json','User-Agent':'source-license-reference'})
        upstream=json.load(urllib.request.urlopen(request,timeout=30));declaration_only=True
    content=base64.b64decode(upstream['content']).decode()
    assert 'MIT' in content
    licenses['upstream/'+upstream['path']]=content
(ROOT/'licenses').mkdir(exist_ok=True)
for path,content in licenses.items():(ROOT/'licenses'/('Pinyin2Hanzi-0.1.1-'+pathlib.PurePosixPath(path).name)).write_text(content)
(ROOT/'legacy-license-provenance.json').write_text(json.dumps({'version':metadata['info']['version'],'license_metadata':metadata['info']['license'],'sdist_url':artifact['url'],'sdist_sha256':hashlib.sha256(raw).hexdigest(),'sdist_contains_license':upstream is None,'upstream_standalone_license_absent':declaration_only,'upstream_license_blob_sha1':upstream['sha'] if upstream else None,'upstream_license_git_url':upstream['_links']['git'] if upstream else None,'licenses':list(licenses)},indent=2)+'\n')
print(json.dumps({'sdist_sha256':hashlib.sha256(raw).hexdigest(),'licenses':list(licenses)}))
