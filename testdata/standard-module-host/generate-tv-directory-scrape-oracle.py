"""Fixed TV directory dispatch order; parsing, NFO/images and storage boundaries controlled."""
import ast,hashlib,json,subprocess
from pathlib import Path
from types import SimpleNamespace as NS
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60'
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',fixed+':app/chain/media.py'],text=True)
owner=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name=='MediaChain')
names={'_handle_tv_directory','_initialize_tv_directory_metadata'}
nodes=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in names]
env={'Path':Path,'settings':NS(RENAME_FORMAT_S0_NAMES=['Specials','SPs']),'MetaInfo':lambda name,**kw:NS(name='',begin_season=2 if name=='Season 2'else None),'ScrapingTarget':NS(SEASON='season',TV='tv'),'logger':NS(warn=lambda *a:None)}
tree=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='MediaChain',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])
exec(compile(ast.fix_missing_locations(tree),'fixed:app/chain/media.py','exec'),env)
cases=[]
for recursive in [False,True]:
 calls=[];obj=env['MediaChain']()
 def listed(**kw):calls.append('list_files');return [NS(type='dir',name='BDMV')]
 obj.storagechain=NS(list_files=listed)
 obj._scrape_nfo_generic=lambda **kw:calls.append('nfo:season:'+str(kw['season_number']))
 obj._scrape_images_generic=lambda **kw:calls.append('images:season:'+str(kw['season_number']))
 obj._handle_tv_directory(fileitem=NS(type='dir',name='Season 2',path='/owned/Season 2/'),filepath=Path('/owned/Season 2'),meta=NS(),mediainfo=NS(),init_folder=True,parent=None,overwrite=False,recursive=recursive)
 cases.append({'recursive':recursive,'calls':calls})
Path(__file__).with_name('tv-directory-scrape-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'source_sha256':hashlib.sha256(source.encode()).hexdigest(),'source_methods':{n.name:[n.lineno,n.end_lineno]for n in nodes},'kind':__doc__,'cases':cases},indent=2)+'\n')
print(cases)
