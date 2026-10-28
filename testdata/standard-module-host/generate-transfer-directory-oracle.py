"""Execute the pinned recursive transfer collector and ordinary storage dispatcher.
Owned providers return controlled FileItem trees. No target code supplies expectations.
"""
import ast, copy, json, re, subprocess
from pathlib import Path
from types import SimpleNamespace

prefix = Path(__file__).with_name('generate-browser-storage-oracle.py')
env = {'__file__': str(prefix)}
exec(compile(prefix.read_text().split('base={')[0], str(prefix), 'exec'), env)
ns, run, FileItem = env['ns'], env['run'], env['FileItem']
fixed = '8a83efeacd3e26b82be495e17d50146e31c96a60'
def owner(path, name):
    text = subprocess.check_output(['git', '-C', str(env['root']), 'show', fixed + ':' + path], text=True)
    return copy.deepcopy(next(n for n in ast.parse(text).body if isinstance(n, ast.ClassDef) and n.name == name))
storage = owner('app/chain/storage.py', 'StorageChain')
run([ast.ClassDef(name='StorageChain', bases=[ast.Name(id='MediaChain', ctx=ast.Load())], keywords=[], body=[n for n in storage.body if isinstance(n, ast.FunctionDef) and n.name in ['list_files', 'get_item', 'get_file_item', 'contains_bluray_subdirectories']], decorator_list=[])], '<fixed:StorageChain>')
Storage = ns['StorageChain']
transfer = owner('app/chain/transfer.py', 'TransferChain')
run([ast.ClassDef(name='TransferChain', bases=[], keywords=[], body=[n for n in transfer.body if isinstance(n, ast.FunctionDef) and n.name == '__get_trans_fileitems'], decorator_list=[])], '<fixed:TransferChain.__get_trans_fileitems>')
ns.update(global_vars=SimpleNamespace(is_system_stopped=False), re=re, Path=Path)
chain = ns['TransferChain']()
root = '{root}/source'
def item(path, kind='file'):
    return dict(storage='local', type=kind, path=path, name=Path(path).name, extension=Path(path).suffix[1:] if kind == 'file' else None, size=26)
top, nested = item(root, 'dir'), item(root+'/Season 1', 'dir')
one, two = item(root+'/Owned.S01E01.mkv'), item(nested['path']+'/Owned.S01E02.mkv')
bluray = item(root+'/Disc', 'dir')
bdmv = item(bluray['path']+'/BDMV', 'dir')
stream = item(bdmv['path']+'/STREAM/00001.m2ts')
cases = [
    ('nested_native', top, {}, {root: [nested, one], nested['path']: [two]}),
    ('root_false', top, {'get_file_item:'+root: False}, {root: [one]}),
    ('list_false', top, {'list_files:'+root: False}, {root: [one]}),
    ('list_tuple_order', top, {'list_files:'+root: (one, nested)}, {root: [], nested['path']: [two]}),
    ('list_merge', top, {'list_files:'+root: [one]}, {root: [nested,one], nested['path']: [two]}),
    ('empty_list_native', top, {'list_files:'+root: []}, {root: [one]}),
    ('single_file', one, {}, {}),
    ('latest_metadata', one, {'get_file_item:'+one['path']: dict(one, size=51, name='provider-name.mkv')}, {}),
    ('bluray_root', bluray, {}, {bluray['path']: [bdmv, item(bluray['path']+'/extra.txt')]}),
    ('bluray_stream', stream, {}, {}),
    ('certificate_only', bluray, {}, {bluray['path']: [item(bluray['path']+'/CERTIFICATE', 'dir')]}),
    ('invalid_list_scalar', top, {'list_files:'+root: True}, {root: [one]}),
]
rows=[]
for name, initial, values, tree in cases:
    trace=[]
    known={x['path']:x for x in [top,nested,one,two,bluray,bdmv,stream]}
    for children in tree.values(): known.update({x['path']:x for x in children})
    def models(v):
        if isinstance(v, list): return [models(x) for x in v]
        if isinstance(v, tuple): return tuple(models(x) for x in v)
        if isinstance(v, dict) and 'path' in v: return FileItem.model_validate(v)
        return v
    def get_file_item(storage, path):
        trace.append(dict(role='plugin', method='get_file_item', path=str(path)))
        return models(copy.deepcopy(values.get('get_file_item:'+str(path))))
    def list_files(fileitem, recursion=False):
        trace.append(dict(role='plugin', method='list_files', path=fileitem.path, recursion=recursion))
        return models(copy.deepcopy(values.get('list_files:'+fileitem.path)))
    class Core:
        def get_priority(self): return 0
        def get_name(self): return '文件整理'
        def get_file_item(self, storage, path):
            trace.append(dict(role='native', method='get_file_item', path=str(path)))
            return models(copy.deepcopy(known.get(str(path))))
        def list_files(self, fileitem, recursion=False):
            trace.append(dict(role='native', method='list_files', path=fileitem.path))
            return models(copy.deepcopy(tree.get(fileitem.path, [])))
    storage=Storage(); storage.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'get_file_item':get_file_item,'list_files':list_files}})
    storage.modulemanager=SimpleNamespace(get_running_modules=lambda method:[Core()]);storage.messagehelper=SimpleNamespace(put=lambda **kw:None);storage.eventmanager=SimpleNamespace(send_event=lambda *a:None)
    ns['StorageChain']=lambda:storage
    error=None;output=None
    try: output=[dict(item=x.model_dump(mode='json'),bluray=b) for x,b in chain._TransferChain__get_trans_fileitems(FileItem.model_validate(initial),None)]
    except Exception as e: error=type(e).__name__
    def encode(v):
        if isinstance(v, tuple): return {'__plugin_module_tuple':list(v)}
        return v
    rows.append(dict(name=name,initial=initial,plugin={k:encode(v) for k,v in values.items()},native_tree=tree,output=output,exception=error,trace=trace))
    assert (error is not None)==(name=='invalid_list_scalar'),(name,error)
Path(__file__).with_name('transfer-directory-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source=['app/chain/transfer.py:2393-2478','app/chain/storage.py:106-116,161-168','app/chain/__init__.py:298-470'],kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed recursive directory and Blu-ray collector cases')
