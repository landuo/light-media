"""Replay fixed wrapper with hash-verified deployed transmission-rpc source.
The captured dependency is public MIT code, not configuration or service data.
"""
import ast,base64,gzip,hashlib,json,pathlib,subprocess,types,sys
from urllib.parse import urlparse
ROOT=pathlib.Path(__file__).resolve().parent;COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
with gzip.open(ROOT/'transmission-rpc-7.0.11-source.json.gz','rt') as f:data=json.load(f)
for row in data.values():assert row['version']=='7.0.11' and hashlib.sha256(row['source'].encode()).hexdigest()==row['sha256']
tree=ast.parse(data['client']['source']);cls=next(x for x in tree.body if isinstance(x,ast.ClassDef) and x.name=='Client');method=next(x for x in cls.body if isinstance(x,ast.FunctionDef) and x.name=='add_torrent');helpers=[x for x in tree.body if isinstance(x,ast.FunctionDef) and x.name in ['remove_unset_value','list_or_none','_single_str_as_list']];read=next(x for x in ast.parse(data['utils']['source']).body if isinstance(x,ast.FunctionDef) and x.name=='_try_read_torrent');nodes=helpers+[read,ast.ClassDef(name='Client',bases=[],keywords=[],body=[method],decorator_list=[])]
def strip(nodes):
 for node in nodes:
  for item in ast.walk(node):
   if isinstance(item,ast.FunctionDef):item.returns=None
   if isinstance(item,ast.arg):item.annotation=None
strip(nodes);ns={'pathlib':pathlib,'base64':base64,'urlparse':urlparse,'RpcMethod':types.SimpleNamespace(TorrentAdd='torrent-add')};exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'actual-transmission-rpc7.0.11','exec'),ns)
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/modules/transmission/transmission.py']);cls=next(x for x in ast.parse(raw).body if isinstance(x,ast.ClassDef));wrapper_method=next(x for x in cls.body if isinstance(x,ast.FunctionDef) and x.name=='add_torrent');strip([wrapper_method]);ns['logger']=types.SimpleNamespace(error=lambda *_:None);exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Wrapper',bases=[],keywords=[],body=[wrapper_method],decorator_list=[])],type_ignores=[])),'fixed-Transmission.add_torrent','exec'),ns)
cases=[]
for content in ['',b'',b'owned-bytes','http://owned.invalid/a','magnet:?xt=urn:btih:'+'a'*40,'file:///owned/a','FILE:///owned/a','not-a-url','http://owned.invalid/a\nhttp://owned.invalid/b','http://[invalid]/a','http://[::1/a','http://[127.0.0.1]/a','http://[vF.owned]/a','http://user:OWNED@host：80/a','http://host.invalid:bad/a','http://owned.invalid/%not-hex']:
 calls=[];client=ns['Client']();client._rpc_version_warning=lambda *_:None;client._request=lambda method,args,**kw:(calls.append({'method':method,'arguments':args}) or {'torrent_added':types.SimpleNamespace(hashString='a'*40)});wrapper=ns['Wrapper']();wrapper.trc=client;accepted=wrapper.add_torrent(content,is_paused=True,download_dir='/owned/downloads',labels=['owned'],cookie='owned=OWNED_COOKIE') is not None;cases.append({'content':content if isinstance(content,str) else {'bytes_hex':content.hex()},'accepted':accepted,'calls':calls})
prior=json.loads((ROOT/'transmission-vendor-add-oracle.json').read_text())
if '--capture' in sys.argv:
 prior['cases']=cases;(ROOT/'transmission-vendor-add-oracle.json').write_text(json.dumps(prior,indent=2)+'\n')
else:assert cases==prior['cases']
print(json.dumps({'status':'passed','source_commit':COMMIT,'vendor':'7.0.11','cases':len(cases),'empty_string_daemon_calls':len(cases[0]['calls']),'empty_bytes_daemon_calls':len(cases[1]['calls'])}))
