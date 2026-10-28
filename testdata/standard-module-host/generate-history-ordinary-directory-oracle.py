"""Execute pinned StorageChain.delete_media_file against owned real temporary
files. Storage operations are controlled boundaries; the caller algorithm is
the unchanged fixed AST. No application or Python runtime is shipped.
"""
import ast, copy, json, shutil, subprocess, tempfile
from pathlib import Path
from types import SimpleNamespace

FIXED = '8a83efeacd3e26b82be495e17d50146e31c96a60'
text = subprocess.check_output(['git', '-C', '/Users/accidia/projects/MoviePilot', 'show', FIXED + ':app/chain/storage.py'], text=True)
tree = ast.parse(text)
cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'StorageChain')
fn = copy.deepcopy(next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == 'delete_media_file'))
fn.decorator_list = []
namespace = dict(Path=Path, schemas=SimpleNamespace(FileItem=object), logger=SimpleNamespace(warn=lambda *a: None, debug=lambda *a: None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[fn], type_ignores=[])), '<fixed:StorageChain.delete_media_file>', 'exec'), namespace)

samples = [
    ('ordinary-directory-root', {'A/B/Disc/Movie.nfo': 'nfo'}),
    ('ordinary-directory-media', {'A/B/Disc/Other/Movie.mkv': 'stream'}),
    ('ordinary-directory-nfo', {'A/B/Disc/Movie.nfo': 'nfo'}),
    ('ordinary-directory-veto', {'A/B/Disc/Movie.nfo': 'nfo'}),
    
    ('other-subtitle', {'A/B/Movie.srt': 'subtitle'}),
    ('nonmedia-nfo', {'A/B/Movie.nfo': 'nfo'}),
    ('nested-audio', {'A/B/extra/Sound.flac': 'audio'}),
    ('temporary-part', {'A/B/Downloading.part': 'partial'}),
    ('empty-parent', {}),
    ('no-associated-nonmedia', {'A/B/Movie.nfo': 'nfo'}),
    ('false-parent', {}),
    ('zero-any-files', {}),
    ('none-any-files', {}),
    ('false-directory-delete', {}),
    ('false-file-delete', {}),
    ('explicit-empty-extensions', {'A/B/Other.bin': 'binary'}),
]
rows = []
for name, other in samples:
    with tempfile.TemporaryDirectory(prefix='fixed-history-delete-') as temporary:
        root = Path(temporary) / 'root'
        path = root / 'A/B/Movie.mkv'
        path.parent.mkdir(parents=True)
        path.write_text('video')
        for relative, content in other.items():
            extra = root / relative
            extra.parent.mkdir(parents=True, exist_ok=True)
            extra.write_text(content)
        if name.startswith('ordinary-directory'):
            path.unlink()
            path = root / 'A/B/Disc'
        trace = []
        def clean(path):
            return str(path).replace(temporary, '$OWNED')
        def item(path):
            return SimpleNamespace(path=str(path), storage='local', type='dir' if path.is_dir() else 'file')
        class Storage:
            def delete_file(self, current):
                p = Path(current.path)
                trace.append(dict(method='delete_file', path=clean(p)))
                if name == 'ordinary-directory-veto': return False
                if not p.exists(): return True
                if name == 'false-file-delete' and p == path or name == 'false-directory-delete' and p.is_dir():
                    return False
                if p.is_dir(): shutil.rmtree(p)
                else: p.unlink()
                return True
            def get_parent_item(self, current):
                trace.append(dict(method='get_parent_item', path=clean(current.path)))
                if name == 'false-parent': return False
                return item(Path(current.path).parent)
            def list_files(self, current, recursion):
                trace.append(dict(method='list_files', path=clean(current.path), recursion=recursion))
                return [item(p) for p in Path(current.path).iterdir()]
            def any_files(self, current, extensions):
                trace.append(dict(method='any_files', path=clean(current.path), extensions=extensions))
                if name == 'zero-any-files': return 0
                if name == 'none-any-files': return None
                entries = list(Path(current.path).rglob('*'))
                if not extensions: return bool(entries)
                return any(p.is_file() and p.suffix.lower() in extensions for p in entries)
            def is_bluray_folder(self, current):
                trace.append(dict(method='is_bluray_folder', path=clean(current.path)))
                return False
        extensions = [] if name == 'explicit-empty-extensions' else ['.mkv', '.!qb', '.part', '.srt', '.flac']
        namespace['settings'] = SimpleNamespace(RMT_MEDIAEXT=extensions[:1], DOWNLOAD_TMPEXT=extensions[1:3], RMT_SUBEXT=extensions[3:4], RMT_AUDIOEXT=extensions[4:])
        namespace['DirectoryHelper'] = lambda: SimpleNamespace(get_dirs=lambda: [] if name == 'no-associated-nonmedia' else [SimpleNamespace(download_path=str(path if name == 'ordinary-directory-root' else root), library_path='')])
        result = namespace['delete_media_file'](Storage(), item(path))
        rows.append(dict(name=name, other=other, result=result, trace=trace, remaining=sorted(str(p.relative_to(root)) for p in root.rglob('*')) if root.exists() else []))

Path(__file__).with_name('history-ordinary-directory-source-oracle.json').write_text(json.dumps(dict(fixed_backend=FIXED, source='app/chain/storage.py:171-235', boundary=__doc__, cases=[r for r in rows if r['name'].startswith('ordinary-directory')]), indent=2) + '\n')
print(len(rows), 'fixed real-temp-file parent cleanup cases')
