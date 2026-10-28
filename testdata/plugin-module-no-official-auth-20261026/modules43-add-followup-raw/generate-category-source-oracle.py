"""Actual fixed qB add_torrent category check, with its qbc I/O recorded."""
import ast
import copy
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

path=Path('/Users/accidia/projects/MoviePilot/app/modules/qbittorrent/qbittorrent.py')
owner=next(n for n in ast.parse(path.read_text()).body if isinstance(n,ast.ClassDef)and n.name=='Qbittorrent')
fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='add_torrent'))
fn.decorator_list=[]
prefix=ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)
ns=dict(logger=SimpleNamespace(error=lambda *a:None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[prefix,ast.ClassDef(name='Qbittorrent',bases=[],keywords=[],body=[fn],decorator_list=[])],type_ignores=[])),str(path),'exec'),ns)
cases=[]
for enabled,category in [(False,'华语电影'),(True,'华语电影'),(True,None)]:
 calls=[]
 server=ns['Qbittorrent']();server._category=enabled;server._sequentail=False;server._first_last_piece=False
 server.qbc=SimpleNamespace(torrents_add=lambda **kwargs:calls.append({k:v for k,v in kwargs.items()if k not in ['torrent_files']})or'Ok.')
 server._Qbittorrent__parse_add_torrent_response=lambda raw:(True,['owned-hash'])
 returned=server.add_torrent(content=b'owned',download_dir='/owned/downloads',category=category,ignore_category_check=False)
 cases.append(dict(enabled=enabled,category=category,calls=calls,returned=returned))
result=dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',source='app/modules/qbittorrent/qbittorrent.py:'+str(fn.lineno),sha256=hashlib.sha256(path.read_bytes()).hexdigest(),kind='actual source category branch; qbc HTTP transport/response parser controlled',cases=cases)
Path(__file__).with_name('category-source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print('fixed qB category cases',len(cases))
