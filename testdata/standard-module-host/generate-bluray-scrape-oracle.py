"""Pinned MediaChain movie-directory scrape, including real temporary NFO IO.

Only media identity, policy and local storage/provider boundaries are controlled.
No light-media implementation supplies expected operations or paths.
"""
import ast, hashlib, json, shutil, subprocess, tempfile, threading
from enum import Enum
from pathlib import Path
from types import SimpleNamespace as NS

FIXED = '8a83efeacd3e26b82be495e17d50146e31c96a60'
source = subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',FIXED+':app/chain/media.py'],text=True)
owner = next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef) and n.name=='MediaChain')
names = {'scrape_metadata_event','scrape_metadata','_handle_movie_scraping','_handle_movie_directory','_get_target_fileitem_and_path','_scrape_nfo_generic','_should_scrape','_save_file','_cleanup_temp_file'}
nodes = [n for n in owner.body if isinstance(n,ast.FunctionDef) and n.name in names]
for n in nodes:
    n.decorator_list = [d for d in n.decorator_list if isinstance(d,ast.Name) and d.id=='staticmethod']
class MediaType(Enum): MOVIE='电影'; TV='电视剧'
class ScrapingTarget(Enum): MOVIE='movie'; TV='tv'; SEASON='season'; EPISODE='episode'
class ScrapingMetadata(Enum): NFO='NFO'; POSTER='poster'; BANNER='banner'; THUMB='thumb'; BACKDROP='backdrop'; LANDSCAPE='landscape'
env = dict(Path=Path,logger=NS(**{n:lambda *a,**k:None for n in ['info','debug','warn','error']}),
           MediaType=MediaType,ScrapingTarget=ScrapingTarget,ScrapingMetadata=ScrapingMetadata,
           scraping_lock=threading.Lock(),MetaInfoPath=lambda p:NS(title=p.name),NamedTemporaryFile=tempfile.NamedTemporaryFile,current_umask=0o022)
tree=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='MediaChain',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])
exec(compile(ast.fix_missing_locations(tree),'fixed:app/chain/media.py','exec'),env)
cases=[]
for mode in ['members','empty_file_list','existing_skip','existing_overwrite']:
    with tempfile.TemporaryDirectory(prefix='owned-source-bluray-scrape-') as temp:
        root=Path(temp)/'Owned (2024)';(root/'BDMV/STREAM').mkdir(parents=True)
        (root/'BDMV/STREAM/00001.m2ts').write_bytes(b'owned stream')
        nfo=root/(root.name+'.nfo')
        if mode.startswith('existing'):nfo.write_text('prior NFO')
        calls=[]
        def item(path):
            p=Path(path)
            return NS(path=str(p),type='dir' if p.is_dir() else 'file',name=p.name,storage='local') if p.exists() else None
        def upload(**kw):
            dst=Path(kw['fileitem'].path)/kw['new_name'];shutil.copyfile(kw['path'],dst)
            calls.append({'method':'upload_file','path':str(dst.relative_to(root)),'bytes':dst.read_text()});return item(dst)
        def nfo_module(**kw):
            calls.append({'method':'metadata_nfo','season':kw['season'],'episode':kw['episode']});return '<movie>owned NFO</movie>'
        obj=env['MediaChain']()
        obj.storagechain=NS(get_item=lambda x:bool(item(x.path)),is_bluray_folder=lambda x:True,
            list_files=lambda **kw:[item(p) for p in Path(kw['fileitem'].path).iterdir()],
            contains_bluray_subdirectories=lambda files:any(f.type=='dir' and f.name in ['BDMV','CERTIFICATE'] for f in files),
            get_file_item=lambda **kw:item(kw['path']),upload_file=upload)
        obj.scraping_policies=NS(option=lambda *args:NS(is_skip=False,is_overwrite=False,type=ScrapingTarget.MOVIE,metadata=ScrapingMetadata.NFO))
        obj.metadata_nfo=nfo_module
        obj._scrape_images_generic=lambda **kw:calls.append({'method':'system_images','path':str(Path(kw['current_fileitem'].path).relative_to(root))})
        data={'fileitem':item(root),'file_list':[] if mode=='empty_file_list' else [str(root/'BDMV/STREAM/00001.m2ts')], 'mediainfo':NS(type=MediaType.MOVIE),'overwrite':mode=='existing_overwrite'}
        obj.scrape_metadata_event(NS(event_data=data))
        cases.append({'name':mode,'calls':calls,'files':{str(p.relative_to(root)):p.read_text() for p in root.rglob('*') if p.is_file()}})
out={'fixed_backend':FIXED,'source_sha256':hashlib.sha256(source.encode()).hexdigest(),'source_methods':{n.name:[n.lineno,n.end_lineno]for n in nodes},'kind':'fixed-source AST with controlled identity/policy/storage; real temp NFO write/upload','cases':cases}
Path(__file__).with_name('bluray-scrape-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(len(cases),'fixed-source directory scrape cases')
