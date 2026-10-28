"""Pinned directory filter/planner with controlled metadata and FileItem input.
The actual planner, association and optional identity comparison execute unchanged.
No target implementation supplies the expected order or inherited metadata.
"""
import ast, copy, json, re, subprocess
from pathlib import Path
from types import SimpleNamespace

prefix = Path(__file__).with_name('generate-browser-storage-oracle.py')
env = {'__file__': str(prefix)}
exec(compile(prefix.read_text().split('base={')[0], str(prefix), 'exec'), env)
ns, run, FileItem = env['ns'], env['run'], env['FileItem']
fixed = '8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):
    return ast.parse(subprocess.check_output(['git','-C',str(env['root']),'show',fixed+':'+path],text=True))
def owner(path, name):
    return copy.deepcopy(next(n for n in source(path).body if isinstance(n,ast.ClassDef) and n.name==name))
transfer = owner('app/chain/transfer.py','TransferChain')
do = next(n for n in transfer.body if isinstance(n,ast.FunctionDef) and n.name=='do_transfer')
names = ['__is_subtitle_file','__is_audio_file','__is_media_file','__is_allow_filesize','__get_file_key','__get_file_stem','__get_subtitle_media_stem','__get_extra_media_stem','__get_related_main_file_key','__normalize_dir_path','__get_dir_key','__get_file_parent_key','__optional_attr_equal','__is_same_media_meta']
helpers = [copy.deepcopy(n) for n in ast.walk(do) if isinstance(n,ast.FunctionDef) and n.name in ['_is_allowed_transfer_item','_filter_allowed_file_items','_append_item','_build_directory_index','_plan_file_items']]
body = ast.parse('matched_episode_format_template=False').body+helpers+ast.parse('return _plan_file_items(_filter_allowed_file_items(items))').body
wrapper = ast.FunctionDef(name='plan',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self'),ast.arg(arg='fileitem'),ast.arg(arg='items')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=body,decorator_list=[])
run([ast.ClassDef(name='TransferChain',bases=[],keywords=[],body=[n for n in transfer.body if isinstance(n,ast.FunctionDef) and n.name in names]+[wrapper],decorator_list=[])],'<fixed:directory planner/filter>')
run([next(n for n in source('app/chain/transfer.py').body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='SUBTITLE_STEM_TAGS' for t in n.targets))],'<fixed:subtitle tags>')
string = owner('app/utils/string.py','StringUtils')
run([ast.ClassDef(name='StringUtils',bases=[],keywords=[],body=[n for n in string.body if isinstance(n,ast.FunctionDef) and n.name in ['clear','clear_upper']],decorator_list=[])],'<fixed:clear_upper>')
ns.update(re=re,continue_callback=None,transfer_exclude_words=[],download_hash=None,DownloadHistoryOper=lambda:None,deepcopy=copy.deepcopy)
chain = ns['TransferChain'](); chain._media_exts=['.mkv','.mp4']; chain._subtitle_exts=['.srt']; chain._audio_exts=['.flac']
chain._is_blocked_by_exclude_words=lambda *a:False
chain._resolve_download_history=lambda **kw:None; chain._get_subscribe_custom_words=lambda value:None
root='{root}/source'
def item(name, **kw):
    return dict(storage='local',type='file',path=root+'/'+name,name=Path(name).name,extension=Path(name).suffix[1:],size=20,**kw)
def meta(path, **kw):
    return dict(_path=path,type='电影',name='Owned',year='2024',**kw)
a,b,s,u,v,z = [item(n) for n in ['Owned.2024.mkv','Other.2024.mkv','Owned.2024.zh.srt','Unmatched.flac','Other.2024.srt','Owned.2024.mp4']]
cases = [
    dict(name='main_related_orphan_order', items=[u,s,b,v,a], sync=True),
    dict(name='ambiguous_no_sync', items=[s,z,a], sync=False),
    dict(name='ambiguous_sync_identity', items=[s,z,a], sync=True),
    dict(name='only_extras', items=[u,s], sync=True),
    dict(name='duplicate_keys', items=[s,a,s,a], sync=True),
    dict(name='separate_parent', items=[s,item('Nested/Owned.2024.mkv'),a], sync=True),
    dict(name='provider_size_above_threshold', items=[s,dict(a,size=2*1024*1024)], sync=True, min_filesize=1),
    dict(name='provider_size_filters_main', items=[s,dict(a,size=0)], sync=True, min_filesize=1),
    dict(name='provider_name_hard_filter', items=[dict(a,name='keep.mkv'),s], sync=True, pattern='^keep'),
    dict(name='identity_optional_fields', items=[u,s,b,v,a], sync=True, metas={u['path']:dict(meta(u['path']),year=None),s['path']:dict(meta(s['path']),begin_episode=1),v['path']:dict(meta(v['path']),type='电视剧')}),
]
rows=[]
for case in cases:
    metas={x['path']:meta(x['path']) for x in case['items']}; metas.update(case.get('metas',{}))
    # Independent names remain independent unless a case deliberately supplies
    # equivalent metadata to exercise sync_extra_files.
    for p,m in metas.items():
        if 'Other' in p: m['name']='Other'
        if 'Unmatched' in p: m['name']='Orphan'
    ns.update(sync_extra_files=case['sync'],min_filesize=case.get('min_filesize',0),has_episode_format_template=bool(case.get('pattern')),formaterHandler=SimpleNamespace(match=lambda name:bool(re.search(case['pattern'],name))) if case.get('pattern') else None)
    ns['_build_file_meta']=ns['_build_path_meta']=lambda path,**kw:SimpleNamespace(**copy.deepcopy(metas[str(path)]))
    planned,inherited=chain.plan(FileItem(storage='local',type='dir',path=root,name='source'),[(FileItem.model_validate(x),False) for x in case['items']])
    rows.append(dict(**case,metadata=metas,output=[x.path for x,_ in planned],inherited={key[1]:value._path for key,value in inherited.items()}))
Path(__file__).with_name('transfer-directory-plan-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source=['app/chain/transfer.py:2685-2731','app/chain/transfer.py:3073-3370','app/utils/string.py:199-228'],kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed directory filter/planner cases')
