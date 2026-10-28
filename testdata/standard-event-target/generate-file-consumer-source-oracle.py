"""Read-only fixed ordinary core consumers; storage/daemon I/O are boundaries."""
import ast
import copy
import hashlib
import json
import threading
from pathlib import Path
from types import SimpleNamespace

root = Path('/Users/accidia/projects/MoviePilot')
paths = [root / 'app/chain/media.py', root / 'app/chain/download.py']
logger = SimpleNamespace(info=lambda *a: None, debug=lambda *a: None,
                         error=lambda *a: None, warn=lambda *a: None)

def load(path, owner_name, name):
    owner = next(n for n in ast.parse(path.read_text()).body
                 if isinstance(n, ast.ClassDef) and n.name == owner_name)
    fn = copy.deepcopy(next(n for n in owner.body
                            if isinstance(n, ast.FunctionDef) and n.name == name))
    fn.decorator_list = []
    ns = dict(Path=Path, logger=logger, scraping_lock=threading.RLock())
    prefix = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[prefix, fn], type_ignores=[])), str(path), 'exec'), ns)
    return ns[name], fn.lineno

def item(path, kind='dir'):
    return SimpleNamespace(storage='local', path=path, type=kind)

def clean(value):
    if isinstance(value, Path): return value.as_posix()
    if isinstance(value, SimpleNamespace): return {k: clean(v) for k, v in vars(value).items()}
    if isinstance(value, (list, tuple)): return [clean(x) for x in value]
    if isinstance(value, dict): return {k: clean(v) for k, v in value.items()}
    return value

scrape, scrape_line = load(paths[0], 'MediaChain', 'scrape_metadata_event')
cases = []
for name, payload, present, bluray in [
    ('no_event', None, True, False),
    ('missing_fileitem', {}, True, False),
    ('missing_storage_item', {'fileitem': item('/owned/root')}, False, False),
    ('single_file', {'fileitem': item('/owned/root/a.mkv', 'file'), 'mediainfo': {'title': 'Owned'}, 'overwrite': True}, True, False),
    ('directory_recursive', {'fileitem': item('/owned/root'), 'meta': {'name': 'Owned'}, 'mediainfo': {'title': 'Owned'}}, True, False),
    ('tree_dedup_order', {'fileitem': item('/owned/root'), 'file_list': ['/owned/root/z/2.mkv', '/owned/root/a/b/1.mkv', '/owned/root/z/2.mkv', '/owned/root/a/3.mkv'], 'mediainfo': {'title': 'Owned'}, 'overwrite': False}, True, False),
    ('bluray_root_only', {'fileitem': item('/owned/root'), 'file_list': ['/owned/root/BDMV/STREAM/00001.m2ts'], 'mediainfo': {'title': 'Owned'}}, True, True),
    ('missing_child_continues', {'fileitem': item('/owned/root'), 'file_list': ['/owned/root/missing/a.mkv', '/owned/root/z.mkv']}, True, False),
]:
    calls = []
    def get_file_item(storage, path):
        calls.append(dict(method='get_file_item', storage=storage, path=clean(path)))
        if 'missing' in str(path): return None
        return item(str(path), 'file' if Path(path).suffix else 'dir')
    storage = SimpleNamespace(get_item=lambda fileitem: calls.append(dict(method='get_item', fileitem=clean(fileitem))) or present,
                              get_parent_item=lambda fileitem: item(str(Path(fileitem.path).parent)),
                              is_bluray_folder=lambda fileitem: calls.append(dict(method='is_bluray_folder', fileitem=clean(fileitem))) or bluray,
                              get_file_item=get_file_item)
    owner = SimpleNamespace(storagechain=storage,
                            scrape_metadata=lambda **kwargs: calls.append(dict(method='scrape_metadata', kwargs=clean(kwargs))))
    error = None
    try: scrape(owner, None if payload is None else SimpleNamespace(event_data=payload))
    except Exception as exc: error = type(exc).__name__
    cases.append(dict(name=name, input=clean(payload), storage_present=present, bluray=bluray, calls=calls, exception=error))

deleted, deleted_line = load(paths[1], 'DownloadChain', 'download_file_deleted')
deleted_cases = []
for name, payload, rows, removal in [
    ('no_event', None, [], True),
    ('src_only', {'src': '/owned/a.mkv'}, [], True),
    ('empty_hash', {'hash': ''}, [], True),
    ('no_torrents', {'hash': 'owned-hash'}, [], True),
    ('listed_remove_false_still_publish', {'hash': 'owned-hash'}, [{'hash': 'owned-hash', 'downloader': 'Owned downloader'}], False),
    ('listed_remove_true_publish', {'hash': 'owned-hash'}, [{'hash': 'owned-hash', 'downloader': 'Owned downloader'}], True),
]:
    calls = []
    def listed(**kwargs):
        calls.append(dict(method='list_torrents', kwargs=kwargs))
        return [SimpleNamespace(model_dump=lambda row=row: row) for row in rows]
    def remove(**kwargs):
        calls.append(dict(method='remove_torrents', kwargs=kwargs, returned=removal))
        return removal
    owner = SimpleNamespace(list_torrents=listed, remove_torrents=remove,
                            eventmanager=SimpleNamespace(send_event=lambda kind, data: calls.append(dict(method='send_event', event=kind, data=data))))
    deleted.__globals__['EventType'] = SimpleNamespace(DownloadDeleted='download.deleted')
    error = None
    try: deleted(owner, None if payload is None else SimpleNamespace(event_data=payload))
    except Exception as exc: error = type(exc).__name__
    deleted_cases.append(dict(name=name, input=payload, rows=rows, removal=removal, calls=calls, exception=error))

result = dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',
              kind='actual fixed ordinary event consumer functions; storage existence/FileItem and list/remove are controlled I/O boundaries, not physical scrape/delete proof',
              source_sha256={str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths},
              scrape=dict(source='app/chain/media.py:'+str(scrape_line), cases=cases),
              deleted=dict(source='app/chain/download.py:'+str(deleted_line), cases=deleted_cases))
Path(__file__).with_name('file-consumer-source-oracle.json').write_text(json.dumps(result, ensure_ascii=False, indent=2)+'\n')
print('fixed file consumers', len(cases), len(deleted_cases))
