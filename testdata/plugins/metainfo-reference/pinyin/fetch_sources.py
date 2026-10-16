import hashlib
import io
import json
import pathlib
import subprocess
import tarfile
import urllib.request

ROOT=pathlib.Path(__file__).resolve().parent
EXPECTED='4fa1bf26d5923bb73e34c63e7ef21fc976c37e016ea13872045bf72335f0a43c'
crate=ROOT/'inputx-pinyin-1.0.2.crate'
if not crate.exists():
    raw=urllib.request.urlopen('https://static.crates.io/crates/inputx-pinyin/inputx-pinyin-1.0.2.crate',timeout=30).read()
    assert hashlib.sha256(raw).hexdigest()==EXPECTED
    crate.write_bytes(raw)
raw=crate.read_bytes();assert hashlib.sha256(raw).hexdigest()==EXPECTED
with tarfile.open(fileobj=io.BytesIO(raw),mode='r:gz') as archive:
    for entry in archive.getmembers():
        path=pathlib.PurePosixPath(entry.name)
        assert not path.is_absolute() and '..' not in path.parts and not entry.issym() and not entry.islnk()
        destination=ROOT/'source'/path
        if entry.isdir():destination.mkdir(parents=True,exist_ok=True)
        elif entry.isfile():destination.parent.mkdir(parents=True,exist_ok=True);destination.write_bytes(archive.extractfile(entry).read())

remote=r'''import ast,hashlib,importlib.metadata,json,pathlib,sys,unicodedata
d=importlib.metadata.distribution('Pinyin2Hanzi')
p=pathlib.Path(d.locate_file('Pinyin2Hanzi/util.py'));raw=p.read_bytes();tree=ast.parse(raw)
licenses={}
for f in d.files or []:
    if 'license' in str(f).lower() or 'copying' in str(f).lower():
        q=pathlib.Path(d.locate_file(f))
        if q.is_file():licenses[str(f)]=q.read_text()
meta_path=pathlib.Path('/app/app/core/meta/metavideo.py');meta_raw=meta_path.read_bytes();meta_tree=ast.parse(meta_raw)
wrapper=next(n for n in ast.walk(meta_tree) if isinstance(n,ast.FunctionDef) and n.name=='__is_pinyin')
ascii_lower_exceptions={str(i):chr(i).lower() for i in range(128,0x110000) if chr(i).lower().isascii()}
whitespace=[i for i in range(0x110000) if chr(i).isspace()]
print(json.dumps({'version':d.version,'source_path':str(p),'source_sha256':hashlib.sha256(raw).hexdigest(),'source':raw.decode(),'licenses':licenses,'wrapper_source_path':str(meta_path),'wrapper_source_sha256':hashlib.sha256(meta_raw).hexdigest(),'wrapper':ast.unparse(wrapper),'python':sys.version,'unicode':unicodedata.unidata_version,'ascii_lower_exceptions':ascii_lower_exceptions,'whitespace':whitespace},ensure_ascii=False))
'''
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129','docker exec -i moviepilot python -'],input=remote,text=True,capture_output=True,check=True)
legacy=json.loads(r.stdout);assert legacy['version']=='0.1.1';assert legacy['source_sha256']=='5e863ce8c401e27ce820fba275ac1f215b26c0f427906da793684b9c660212b7'
(ROOT/'legacy-installed-source.json').write_text(json.dumps(legacy,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'crate_sha256':EXPECTED,'legacy_version':legacy['version'],'legacy_source_sha256':legacy['source_sha256'],'legacy_licenses':list(legacy['licenses'])}))
