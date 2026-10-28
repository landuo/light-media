"""Execute pinned torrent completion guards and native tag wrappers unchanged.
Job state and protocol objects are controlled inputs, not target expectations.
"""
import ast, contextlib, copy, json, subprocess
from pathlib import Path
from types import SimpleNamespace

root = '/Users/accidia/projects/MoviePilot'
fixed = '8a83efeacd3e26b82be495e17d50146e31c96a60'
def methods(path, cls, names):
    source = ast.parse(subprocess.check_output(['git','-C',root,'show',fixed+':'+path], text=True))
    owner = next(n for n in source.body if isinstance(n,ast.ClassDef) and n.name==cls)
    selected = [copy.deepcopy(n) for n in owner.body if isinstance(n,ast.FunctionDef) and n.name in names]
    assert len(selected)==len(names)
    return ast.ClassDef(name=cls,bases=[],keywords=[],body=selected,decorator_list=[])
ns = dict(Optional=__import__('typing').Optional,Union=__import__('typing').Union,List=__import__('typing').List, job_lock=contextlib.nullcontext(), logger=SimpleNamespace(debug=lambda *a:None,error=lambda *a:None))
nodes = [methods('app/chain/transfer.py','TransferChain',['__mark_torrent_completed_if_done','__is_torrent_download_completed'])]
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<fixed:transfer completion>','exec'),ns)
cases = [
    dict(name='no_history', history=False, progress=[100]),
    dict(name='empty_hash', hash='', progress=[100]),
    dict(name='unfinished_task', done=[False], progress=[100]),
    dict(name='empty_result', progress=[]),
    dict(name='partial_download', progress=[99.99]),
    dict(name='null_progress', progress=[None]),
    dict(name='mixed_progress', progress=[100,50]),
    dict(name='query_error', progress=[100], query_error=True),
    dict(name='new_task_during_query', done=[True,False], progress=[100]),
    dict(name='all_successful', progress=[100]),
    dict(name='all_failed_but_done', progress=[100,101]),
]
rows=[]
for case in cases:
    trace=[]; checks=iter(case.get('done',[True,True])); chain=ns['TransferChain']()
    def done(h):
        trace.append('tasks_done'); return next(checks)
    def listing(**kwargs):
        trace.append(dict(list_torrents=kwargs))
        if case.get('query_error'): raise RuntimeError('owned query failure')
        return [SimpleNamespace(progress=p) for p in case['progress']]
    chain.jobview=SimpleNamespace(is_torrent_done=done)
    chain.list_torrents=listing
    chain.transfer_completed=lambda **kw:trace.append(dict(transfer_completed=kw))
    chain._TransferChain__mark_torrent_completed_if_done(case.get('hash','a'*40),'Owned completion',case.get('history',True))
    rows.append(dict(**case,trace=trace,marked=any(isinstance(v,dict) and 'transfer_completed' in v for v in trace)))
native=[]
for cls,path in [('QbittorrentModule','qbittorrent'),('TransmissionModule','transmission')]:
    # The actual class names are read, not inferred from the directory.
    tree=ast.parse(subprocess.check_output(['git','-C',root,'show',fixed+':app/modules/'+path+'/__init__.py'],text=True))
    cls=next(n.name for n in tree.body if isinstance(n,ast.ClassDef))
    exec(compile(ast.fix_missing_locations(ast.Module(body=[methods('app/modules/'+path+'/__init__.py',cls,['transfer_completed'])],type_ignores=[])),'<fixed:native completion>','exec'),ns)
    for present in [False,True]:
        trace=[]; obj=ns[cls]()
        server=SimpleNamespace(set_torrents_tag=lambda **kw:trace.append(kw),get_torrent_tags=lambda **kw:['light-media','kept'],set_torrent_tag=lambda **kw:trace.append(kw))
        obj.get_instance=lambda name:server if present else None
        result=obj.transfer_completed(hashs='a'*40,downloader='Owned completion')
        native.append(dict(kind=path,present=present,trace=trace,result=result))
helpers=[]
for cls,path,names in [('Qbittorrent','qbittorrent',['set_torrents_tag']),('Transmission','transmission',['set_torrent_tag','get_torrent_tags'])]:
    exec(compile(ast.fix_missing_locations(ast.Module(body=[methods('app/modules/'+path+'/'+path+'.py',cls,names)],type_ignores=[])),'<fixed:protocol helpers>','exec'),ns)
    for fail in [False,True]:
        writes=[];logs=[];obj=ns[cls]()
        ns['logger']=SimpleNamespace(error=lambda text:logs.append(text))
        def write(**kw):
            if fail: raise RuntimeError('owned protocol error')
            writes.append(kw)
        if path=='qbittorrent':
            obj.qbc=SimpleNamespace(torrents_add_tags=write)
            result=obj.set_torrents_tag('a'*40,['已整理'])
        else:
            obj.trc=SimpleNamespace(change_torrent=write,get_torrents=lambda **kw:[SimpleNamespace(labels=[' light-media ','kept','kept'])]);obj._trarg=[]
            tags=obj.get_torrent_tags('a'*40)
            result=obj.set_torrent_tag('a'*40,tags+['已整理'])
            for w in writes:w['labels']=sorted(w['labels']) # Python set has no stable order.
        helpers.append(dict(kind=path,fail=fail,writes=writes,logs=len(logs),result=result,raised=False))
Path(__file__).with_name('transfer-completed-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source=['app/chain/transfer.py:661-673,1338,1427-1474,1888,3679','app/modules/qbittorrent/__init__.py:455-465','app/modules/transmission/__init__.py:454-472','app/modules/qbittorrent/qbittorrent.py:336-347','app/modules/transmission/transmission.py:168-201'],cases=rows,native=native,helpers=helpers),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed completion guards;',len(native),'native wrappers;',len(helpers),'protocol helpers')
