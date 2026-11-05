"""Execute pinned ordinary storage methods in an isolated temporary directory.

Only import-time app infrastructure is replaced. Method bodies are compiled
unchanged from the fixed Git objects, with real pathlib/shutil filesystem IO.
This is source-method replay, not a running MoviePilot service.
"""
import ast
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import tempfile
from contextlib import nullcontext
from types import SimpleNamespace
from typing import List, Optional, Tuple

SHA = "8a83efeacd3e26b82be495e17d50146e31c96a60"
REPO = "/Users/accidia/projects/MoviePilot"
sources = {}
def source(path):
    raw = subprocess.check_output(["git", "-C", REPO, "show", f"{SHA}:{path}"])
    sources[path] = hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)

class FileItem(SimpleNamespace):
    pass

environment = dict(Path=Path, os=os, shutil=shutil, List=List, Optional=Optional,
                   schemas=SimpleNamespace(FileItem=FileItem), FileItem=FileItem,
                   logger=SimpleNamespace(warn=lambda *a: None, error=lambda *a: None))
def extract(tree, original, name, methods, extra=None):
    cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == original)
    selected = [n for n in cls.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in methods]
    node = ast.ClassDef(name=name, bases=[], keywords=[], body=selected, decorator_list=[])
    module = ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[]))
    exec(compile(module, original, "exec"), environment)
    result = environment[name]
    for key, value in (extra or {}).items(): setattr(result, key, value)
    return result

system = extract(source("app/utils/system.py"), "SystemUtils", "SystemUtils",
                 {"list_sub_directory", "list_sub_file"}, {"is_windows": staticmethod(lambda: False)})
local = extract(source("app/modules/filemanager/storages/local.py"), "LocalStorage", "LocalStorage",
                {"__get_fileitem", "__get_diritem", "list", "detail", "delete"},
                {"schema": SimpleNamespace(value="local")})()
manager = extract(source("app/modules/filemanager/__init__.py"), "FileManagerModule", "FileManagerModule", {"list_files"},
                  {"_support_storages": {"local"}, "_FileManagerModule__get_storage_oper": lambda self, _: local})()
rows = []
with tempfile.TemporaryDirectory(prefix="mp-host-services-oracle-") as folder:
    root = Path(folder)
    (root / "nested").mkdir()
    (root / "nested" / "child.txt").write_text("child")
    (root / "top.txt").write_text("top")
    (root / ".hidden").mkdir()
    (root / ".hidden" / "excluded.txt").write_text("hidden")
    for case, path, recursive in [("flat", root, False), ("recursive", root, True),
                                   ("file", root / "top.txt", False), ("missing", root / "absent", False)]:
        found = manager.list_files(FileItem(storage="local", path=str(path)), recursive)
        rows.append(dict(case=case, items=[dict(type=i.type, path=str(Path(i.path).relative_to(root))) for i in found]))
    for case, path in [("delete_file", root/"top.txt"), ("delete_directory", root/"nested"), ("delete_missing", root/"absent")]:
        returned = local.delete(FileItem(storage="local", path=str(path)))
        rows.append(dict(case=case, returned=returned, exists=path.exists()))

media_tree = source("app/utils/media.py")
media_nodes = [n for n in media_tree.body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "MEDIA_SOURCE_ALIASES" for t in n.targets) or isinstance(n, ast.FunctionDef) and n.name == "normalize_media_source"]
exec(compile(ast.fix_missing_locations(ast.Module(body=media_nodes, type_ignores=[])), "media.py", "exec"), environment)
environment.update(Tuple=Tuple, MetaBase=SimpleNamespace, MediaInfo=SimpleNamespace,
                   MediaType=SimpleNamespace(TV="电视剧", MOVIE="电影"), fresh=lambda _: nullcontext())
captured = []
def run_module(self, method, **kwargs):
    captured.append({k: vars(v) if isinstance(v, SimpleNamespace) else v for k, v in kwargs.items()})
    return SimpleNamespace(recognize_cache_hit=True)
chain = extract(source("app/chain/__init__.py"), "ChainBase", "ChainBase",
                {"_resolve_media_source_params", "recognize_media"}, {"run_module": run_module})()
recognition = []
for request in [dict(tmdbid=123,mtype="电影",meta=dict(tmdbid=999,media_source="douban",media_id="12345")),
                dict(source="tmdb",mediaid="456",tmdbid=123,mtype="电视剧",cache=False),
                dict(tmdbid=123,doubanid="999"),
                dict(meta=dict(type="电视剧",tmdbid=0,doubanid="42",episode_group="group")),
                dict(source="anilist",mediaid="123",doubanid="999")]:
    arguments = dict(request)
    if "meta" in arguments: arguments["meta"] = SimpleNamespace(**arguments["meta"])
    chain.recognize_media(**arguments)
    recognition.append(dict(request=request, kwargs=captured[-1]))

environment["logger"] = SimpleNamespace(warn=lambda *a: None, error=lambda *a: None, info=lambda *a: None, debug=lambda *a: None)
environment["_DATE_RE"] = re.compile(r"^\d{4}-\d{2}-\d{2}$")
tmdb_class = extract(source("app/modules/themoviedb/__init__.py"), "TheMovieDbModule", "TheMovieDbModule",
                     {"_get_info_by_tmdbid", "_disambiguate_by_meta", "_fill_group_season_info", "_process_episode_groups"})
disambiguation = []
tv = dict(id=123, name="Television", original_name="Television", first_air_date="2024-01-01", media_type="电视剧")
movie = dict(id=123, title="Movie", original_title="Movie", release_date="2025-01-01", media_type="电影")
for case, tv_info, movie_info, meta, mtype in [
        ("no_meta_ambiguous", tv, movie, None, None),
        ("title_tv", tv, movie, dict(cn_name="Television", en_name="", year=None, type=None), None),
        ("year_movie", tv, movie, dict(cn_name="", en_name="", year="2025", type=None), None),
        ("tie_type_tv", tv, movie, dict(cn_name="", en_name="", year=None, type="电视剧"), None),
        ("tie_unknown", tv, movie, dict(cn_name="", en_name="", year=None, type=None), None),
        ("tv_only", tv, None, None, None),
        ("movie_only", None, movie, None, None),
        ("explicit_movie", tv, movie, None, "电影")]:
    calls = []
    def get_info(mtype, tmdbid):
        calls.append(mtype)
        return tv_info if mtype == "电视剧" else movie_info
    instance = tmdb_class()
    instance.tmdb = SimpleNamespace(get_info=get_info)
    selected = instance._get_info_by_tmdbid(123, mtype, SimpleNamespace(**meta) if meta else None)
    disambiguation.append(dict(case=case, tv=tv_info, movie=movie_info, meta=meta, mtype=mtype,
                               lookup_order=calls, selected_type=selected.get("media_type") if selected else None))

api = extract(source("app/modules/themoviedb/tmdbapi.py"), "TmdbApi", "TmdbApi", {"get_tv_group_seasons"})()
group_rows = [dict(id="season-x", order=2, name="Owned group", episodes=[dict(id=10, episode_number=99, air_date="2025-01-02"), dict(id=11, episode_number=102, air_date="2025-01-03")]), dict(order=3, episodes=[])]
api.tv = SimpleNamespace(group_episodes=lambda _: group_rows)
normalized_groups = api.get_tv_group_seasons("selected")
groups = []
for case, selected, existing in [("explicit_group", "selected", normalized_groups), ("automatic_years", None, [])]:
    instance = tmdb_class()
    calls = []
    def get_group_seasons(group_id):
        calls.append(group_id)
        return normalized_groups
    instance.tmdb = SimpleNamespace(get_tv_group_seasons=get_group_seasons)
    model = SimpleNamespace(type="电视剧", episode_groups=[dict(id="automatic", type=6)])
    returned = instance._process_episode_groups(model, selected, existing)
    groups.append(dict(case=case, selected=selected, initial=dict(type="电视剧", episode_groups=[dict(id="automatic", type=6)]),
                       provider_groups=group_rows, normalized_groups=normalized_groups, calls=calls, result=vars(returned)))

print(json.dumps(dict(source_commit=SHA, kind="isolated_source_method_replay", sources=sources, cases=rows, recognition=recognition, disambiguation=disambiguation, groups=groups), ensure_ascii=False, indent=2))
