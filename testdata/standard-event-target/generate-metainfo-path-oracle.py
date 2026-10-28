"""Read-only fixed MetaInfoPath AST + public ABI wheel; no app imports/startup.

Rust cases execute the real extension, fixed path entry and fixed binding. Empty
MetaVideo/Anime constructors are controlled boundaries: only overwritten binding
attributes are asserted. Legacy cases replay captured real title objects through
the fixed Python path entry/helper/merge, without claiming a live Python parser.
"""
import ast
import copy
import gzip
import hashlib
import json
import re
import sys
from enum import Enum
from pathlib import Path
from types import SimpleNamespace

ROOT = Path('/Users/accidia/projects/MoviePilot')
TARGET = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(TARGET/'work/plugin-no-official-auth-20261026/metainfo-path-rust-runtime'))
import moviepilot_rust

source = ROOT/'app/core/metainfo.py'
tree = ast.parse(source.read_text())
names = {'_meta_from_rust', 'MetaInfoPath'}
nodes = [copy.deepcopy(n) for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in names]
assert len(nodes) == 2
class MediaType(Enum):
    MOVIE = '电影'
    TV = '电视剧'
    COLLECTION = '合集'
    UNKNOWN = '未知'
class Shell:
    def __init__(self, _=''): pass
    @property
    def name(self):
        cn = getattr(self, 'cn_name', None)
        en = getattr(self, 'en_name', None)
        return cn if cn and all(c == ' ' or '\u4e00' <= c <= '\u9fff' for c in cn) else en or cn
class Anime(Shell): pass
ns = dict(MetaVideo=Shell, MetaAnime=Anime, MediaType=MediaType)
for n in tree.body:
    if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == '_UHD_BLURAY_RE' for t in n.targets):
        exec(compile(ast.Module(body=[n], type_ignores=[]), str(source), 'exec'), dict(re=re), ns)
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), *nodes], type_ignores=[])), str(source), 'exec'), ns)
helper = ROOT/'app/core/meta/infopath.py'
helper_tree = ast.parse(helper.read_text())
helper_tree.body = [n for n in helper_tree.body if not isinstance(n, (ast.Import, ast.ImportFrom))]
ns.update(re=re, StringUtils=SimpleNamespace(is_all_chinese=lambda s: all(c == ' ' or '\u4e00' <= c <= '\u9fff' for c in s)))
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), *helper_tree.body], type_ignores=[])), str(helper), 'exec'), ns)
meta_base = ROOT/'app/core/meta/metabase.py'
owner = next(n for n in ast.parse(meta_base.read_text()).body if isinstance(n, ast.ClassDef) and n.name == 'MetaBase')
merge = next(copy.deepcopy(n) for n in owner.body if isinstance(n, ast.FunctionDef) and n.name == 'merge')
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), merge], type_ignores=[])), str(meta_base), 'exec'), ns)
Shell.merge = ns['merge']

def clean(obj):
    if isinstance(obj, Enum): return obj.value
    if isinstance(obj, dict): return {k: clean(v) for k, v in obj.items()}
    if isinstance(obj, list): return [clean(v) for v in obj]
    return obj

media_exts = '.mp4 .mkv .avi .mov .ts .iso .m2ts .wmv .flv .srt .ass .ssa .sub .vtt .mp3 .flac .aac .wav'.split()
cases = []
for name, path, words, local in [
    ('season_parent_and_grandparent', '/owned/Show.2024/Season 02/S02E03.mkv', [], None),
    ('auxiliary_filename_uses_english_parent', '/owned/Owned.Movie.2024.1080p/简中字幕.mkv', [], None),
    ('chinese_parent_keeps_auxiliary_name', '/owned/电影名称.2024/简中字幕.mkv', [], None),
    ('file_title_wins_parent', '/owned/Parent.Movie.2020/Actual.Movie.2024.mkv', [], None),
    ('movie_does_not_merge_tv_directory', '/owned/Show.S02.2024/Actual.Movie.2020.mkv', [], None),
    ('explicit_id_and_season_from_path', '/owned/Show.{tmdb-900004}/Season 02/S02E03.mkv', [], None),
    ('global_custom_word_applied', '/owned/Raw.Movie.2024/Raw.Movie.mkv', ['Raw => Original'], None),
    ('present_empty_local_overrides_global', '/owned/Raw.Movie.2024/Raw.Movie.mkv', ['Raw => Original'], []),
]:
    options = dict(custom_words=words if local is None else local, media_exts=media_exts, streaming_platforms={})
    ns.update(_requires_python_metainfo=lambda *_: False, _rust_parse_options=lambda _: options,
              rust_accel=SimpleNamespace(parse_metainfo_path=lambda p, opts: moviepilot_rust.parse_metainfo_path_fast(p, opts)))
    result = ns['MetaInfoPath'](Path(path), local)
    cases.append(dict(name=name, path=path, mode='rust', words=words, local_words=local, expected=clean(vars(result))))

legacy = {}
capture = TARGET/'testdata/plugins/metainfo-title-oracle.ndjson.gz'
with gzip.open(capture, 'rt') as f:
    for line in f:
        r = json.loads(line)
        if r['record'] != 'case': continue
        d = r['data']
        if d['mode'] == 'legacy' and not d['global_words'] and d['local_words'] is None and d.get('result'):
            legacy.setdefault(d['title'], d['result']['attributes'])
def captured(title, custom_words=None):
    out = Shell()
    out.__dict__.update(copy.deepcopy(legacy[title]))
    out.type = MediaType(out.type)
    return out
for name, path in [
    ('legacy_tv_preserves_file_range', '/Synthetic Film 2024/Synthetic.Show.S01E02.1080p.WEB-DL.H265.HDR10-UBWEB'),
    ('legacy_movie_ignores_tv_parent', '/Synthetic Show S01-S03 1080p WEBRip/Synthetic Film 2024'),
    ('legacy_empty_leaf_parent_chain', '/Synthetic Film 2024/'),
]:
    ns.update(_requires_python_metainfo=lambda *_: True, MetaInfo=captured)
    result = ns['MetaInfoPath'](Path(path), None)
    cases.append(dict(name=name, path=path, mode='legacy', words=[], local_words=None, expected=clean(vars(result))))
wheel = TARGET/'work/_brush_flow_port/next/revision5/public-rust-0.2.6/moviepilot_rust-0.2.6-cp311-abi3-macosx_11_0_arm64.whl'
result = dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',
              runtime=sys.version, kind=__doc__,
              wheel_version='0.2.6', wheel_sha256=hashlib.sha256(wheel.read_bytes()).hexdigest(),
              dependency_requirement='moviepilot-rust~=0.2.4',
              wheel_source_commit='017b3a0e4ce2415d641c0eeb84ac907f9f10450c',
              source_sha256={str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in [source, helper, meta_base]},
              captured_legacy_title_sha256=hashlib.sha256(capture.read_bytes()).hexdigest(),
              media_exts=media_exts, cases=cases)
Path(__file__).with_name('metainfo-path-oracle.json').write_text(json.dumps(result, ensure_ascii=False, indent=2)+'\n')
print('fixed path rust ABI cases', 8, 'captured-title Python path replay cases', 3)
