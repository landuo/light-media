"""Replay fixed Trimemedia refresh bodies and fnapi HTTP construction.

The module methods are unchanged AST bodies. Host authentication and network
are isolated finite fixtures; path resolution uses the actual local filesystem.
"""
import ast
from dataclasses import dataclass
from enum import Enum
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
from typing import List, Optional, Union

SHA="8a83efeacd3e26b82be495e17d50146e31c96a60"
REPO="/Users/accidia/projects/MoviePilot"
sources={}
def read(path):
    raw=subprocess.check_output(["git","-C",REPO,"show",SHA+":"+path])
    sources[path]=hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)
env=dict(Path=Path,List=List,Optional=Optional,Union=Union,json=json,dataclass=dataclass,Enum=Enum,
         schemas=SimpleNamespace(RefreshMediaItem=SimpleNamespace),fnapi=SimpleNamespace(MediaDb=SimpleNamespace),
         logger=SimpleNamespace(info=lambda *a:None,error=lambda *a:None),settings=SimpleNamespace(USER_AGENT="owned source UA"),Type=type("Type",(),{}))
def extract(tree,name,methods):
    cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==name)
    body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in methods]
    node=ast.ClassDef(name=name,bases=[],keywords=[],body=body,decorator_list=[])
    exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),name,"exec"),env)
    return env[name]
module=extract(read("app/modules/trimemedia/trimemedia.py"),"TrimeMedia",{"refresh_root_library","refresh_library_by_items","__match_library_by_path"})
rows=[]
with tempfile.TemporaryDirectory(prefix="source-media-refresh-") as folder:
    root=Path(folder);library=root/"library";(library/"nested").mkdir(parents=True)
    (root/"alias").symlink_to(library/"nested",target_is_directory=True)
    (root/"loop").symlink_to(root/"loop",target_is_directory=True)
    (root/"lib\\name").mkdir()
    def resolve_token(value):
        return value.replace("$relative_root",os.path.relpath(root)).replace("$root",str(root))
    samples=[
        ("matched",["$root/library/movie.mkv"],True,True,True,True,False),
        ("deduplicated",["$root/library/one.mkv","$root/library/two.mkv"],True,True,True,True,False),
        ("unmatched_fallback",["$root/elsewhere/movie.mkv"],True,True,True,True,False),
        ("none_path_fallback",[None],True,True,True,True,False),
        ("empty_items",[],True,True,True,True,False),
        ("partial_failure_fallback",["$root/library/movie.mkv"],True,True,False,True,False),
        ("all_failure_false",["$root/library/movie.mkv"],True,True,False,False,False),
        ("not_authenticated_none",["$root/library/movie.mkv"],False,True,True,True,False),
        ("not_admin_false",["$root/library/movie.mkv"],True,False,True,True,False),
        ("source_root",None,True,True,True,True,True),
        ("resolved_alias_parent",["$root/alias/../missing/movie.mkv"],True,True,True,True,False),
    ]
    for case,paths,directories in [
        ("root_target_fallback",["/"],["$root/library"]),
        ("root_library_matches",["$root/library/movie.mkv"],["/"]),
        ("backslash_filename",["$root/library/movie\\part.mkv"],["$root/library"]),
        ("backslash_library",["$root/lib\\name/movie.mkv"],["$root/lib\\name"]),
        ("none_stops_before_loop",[None,"$root/loop/movie.mkv"],["$root/library"]),
        ("unmatched_stops_before_loop",["$root/outside/movie.mkv","$root/loop/movie.mkv"],["$root/library"]),
        ("matched_stops_before_bad_directory",["$root/library/movie.mkv"],["$root/library","$root/loop"]),
        ("relative_paths",["$relative_root/library/movie.mkv"],["$relative_root/library"]),
        ("empty_directories_skip_resolution",["$root/loop/movie.mkv"],[]),
    ]:
        samples.append((case,paths,True,True,True,True,False,directories))
    for sample in samples:
        case,paths,authenticated,admin,partial,all_result,is_root=sample[:7]
        directories=sample[7] if len(sample)>7 else ["$root/library"]
        trace=[];instance=module()
        instance.is_authenticated=lambda:authenticated
        instance._userinfo=SimpleNamespace(is_admin=1 if admin else 0)
        instance._libraries={"owned-guid":SimpleNamespace(guid="owned-guid",name="Owned",dir_list=[resolve_token(d) for d in directories])}
        def task_running(): trace.append("preflight");return False
        def scan(lib): trace.append("scan:owned-guid");return partial
        def scanall(): trace.append("scanall");return all_result
        instance._api=SimpleNamespace(task_running=task_running,mdb_scan=scan,mdb_scanall=scanall)
        items=None if paths is None else [SimpleNamespace(target_path=Path(resolve_token(p)) if p is not None else None) for p in paths]
        result=instance.refresh_root_library() if is_root else instance.refresh_library_by_items(items)
        rows.append(dict(case=case,paths=paths,directories=directories,authenticated=authenticated,admin=admin,partial_result=partial,all_result=all_result,root=is_root,result=result,trace=trace))

api_type=extract(read("app/modules/trimemedia/api.py"),"Api",{"request","mdb_scan","mdb_scanall","task_running"})
api=api_type();api._host="https://owned.invalid/v";api._api_path="/api/v1";api._token="owned synthetic token"
api._Api__get_authx=lambda *args:"owned synthetic signature"
wire=[]
def request(**kwargs):
    wire.append(dict(method=kwargs["method"],path=kwargs["url"].split("owned.invalid",1)[1],body=kwargs["data"],content_type=kwargs["headers"].get("Content-Type")))
    return SimpleNamespace(json=lambda:dict(code=0,data=True))
api._request_utils=SimpleNamespace(request=request)
assert api.task_running() is True
assert api.mdb_scan(SimpleNamespace(guid="owned-guid")) is True
assert api.mdb_scanall() is True
print(json.dumps(dict(source_commit=SHA,classification="isolated fixed module+API AST replay; actual Path.resolve filesystem; no real fnOS service",sources=sources,cases=rows,wire=wire),indent=2))
