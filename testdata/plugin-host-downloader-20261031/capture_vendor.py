"""Replay only the two consumed methods from the fixed, hash-checked wheel."""
import ast, hashlib, io, json, pathlib, types, urllib.request, zipfile
root=pathlib.Path(__file__).resolve().parent
provenance=json.loads((root/'vendor-provenance.json').read_text())
body=urllib.request.urlopen(provenance['url'],timeout=30).read()
assert hashlib.sha256(body).hexdigest()==provenance['wheel_sha256']
archive=zipfile.ZipFile(io.BytesIO(body))
raw=archive.read(provenance['source_path'])
assert hashlib.sha256(raw).hexdigest()==provenance['source_sha256']
for path in archive.namelist():
 if path.endswith('/LICENSE'):
  (root/'vendor-LICENSE.txt').write_bytes(archive.read(path));break
source=raw.decode();tree=ast.parse(source)
methods={m.name:m for n in tree.body if isinstance(n,ast.ClassDef) for m in n.body if isinstance(m,ast.FunctionDef) and m.name in ['torrents_reannounce','torrents_info','torrents_delete_tags']}
for name,method in methods.items():
 short={'torrents_info':'info','torrents_reannounce':'reannounce','torrents_delete_tags':'delete-tags'}[name]
 (root/f'vendor-{short}-source.py.txt').write_text(ast.get_source_segment(source,method)+'\n')
 method.returns=None;method.decorator_list=[]
 for arg in method.args.args+method.args.kwonlyargs:arg.annotation=None
 if method.args.kwarg:method.args.kwarg.annotation=None
 ns={'APINames':types.SimpleNamespace(Torrents='torrents'),'TorrentInfoList':list,'v':lambda s:tuple(int(i) for i in s.split('.'))}
 exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),provenance['source_path'],'exec'),ns)
 class Recorder:
  def __init__(self,version):self.version=version
  def app_web_api_version(self):return self.version
  def _list2string(self,value,sep):return None if value is None else sep.join(value)
  def _post(self,**kwargs):self.call=kwargs
  def _post_cast(self,**kwargs):self.call=kwargs;return []
 result={'kind':'isolated fixed vendor AST replay','version':provenance['version'],'line':method.lineno}
 if short=='reannounce':
  r=Recorder('2.11.0');ns[name](r,['a'*40,'b'*40]);result['call']=r.call
 elif short=='delete-tags':
  r=Recorder('2.11.0');ns[name](r,tags=['alpha','beta']);result['call']=r.call
 else:
  cases=[]
  for version in ['2.10.0','2.11.0']:
   for value in ['paused','stopped','resumed','running','completed']:
    r=Recorder(version);ns[name](r,status_filter=value);cases.append({'webapi_version':version,'source_filter':value,'wire_filter':r.call['data']['filter']})
  result['cases']=cases
 (root/f'vendor-{short}-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'package':provenance['package'],'version':provenance['version'],'wheel_sha256':provenance['wheel_sha256'],'replayed':list(methods)}))
