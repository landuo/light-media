"""Fixed ChainBase wrappers/dispatcher; provider outputs are owned boundaries."""
import base64
import ast
import copy
import hashlib
import inspect
import json
from pathlib import Path
import traceback
from types import FunctionType, SimpleNamespace
from typing import Any, Optional, Union, get_type_hints

root = Path("/Users/accidia/projects/MoviePilot")
path = root / "app/chain/__init__.py"
owner = next(n for n in ast.parse(path.read_text()).body
             if isinstance(n, ast.ClassDef) and n.name == "ChainBase")
names = {"__is_valid_empty", "__handle_plugin_error", "__handle_system_error",
         "__handle_rate_limit_error", "__execute_plugin_modules",
         "__execute_system_modules", "run_module", "start_torrents",
         "stop_torrents", "remove_torrents"}
nodes = [copy.deepcopy(n) for n in owner.body
         if isinstance(n, ast.FunctionDef) and n.name in names]
assert len(nodes) == len(names)
object_path = root / "app/utils/object.py"
check = next(n for n in ast.walk(ast.parse(object_path.read_text()))
             if isinstance(n, ast.FunctionDef) and n.name == "check_signature")
check.decorator_list = []
object_ns = dict(inspect=inspect, get_type_hints=get_type_hints,
                 FunctionType=FunctionType)
exec(compile(ast.fix_missing_locations(ast.Module(body=[check], type_ignores=[])),
             "fixed-object-signature", "exec"), object_ns)
class RateLimitExceededException(Exception):
    pass
logger = SimpleNamespace(info=lambda *a: None, debug=lambda *a: None,
                         error=lambda *a: None)
ns = dict(inspect=inspect, traceback=traceback, logger=logger,
          ObjectUtils=SimpleNamespace(check_signature=object_ns["check_signature"]),
          RateLimitExceededException=RateLimitExceededException,
          EventType=SimpleNamespace(SystemError="system.error"))
module = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0),
                         ast.ClassDef(name="ChainBase", bases=[], keywords=[], body=nodes, decorator_list=[])], type_ignores=[])
exec(compile(ast.fix_missing_locations(module), str(path), "exec"), ns)

def clean(value):
    if isinstance(value, bytes):
        return {"__plugin_module_bytes": base64.b64encode(value).decode()}
    if isinstance(value, tuple):
        return {"__plugin_module_tuple": [clean(x) for x in value]}
    if isinstance(value, list):
        return [clean(x) for x in value]
    if isinstance(value, dict):
        return {k: clean(v) for k, v in value.items()}
    return value

samples = [
    ("null_core", "start", None, False, True, False),
    ("false_terminal", "pause", False, False, True, False),
    ("empty_dict_terminal", "remove_keep_data", {}, False, True, False),
    ("empty_list_ignores_core_bool", "remove_with_data", [], False, True, False),
    ("list_core_exception", "pause", ["owned"], False, True, True),
    ("plugin_exception_core", "start", None, True, True, False),
    ("all_null_tuple_core", "pause", (None, None), False, True, False),
    ("true_terminal", "start", True, False, True, False),
    ("null_core_rejected", "pause", None, False, False, False),
    ("list_core_rejected", "pause", ["owned"], False, False, False),
    ("empty_bytes_terminal", "pause", b"", False, True, False),
    ("bytes_terminal", "start", b"owned", False, True, False),
]
cases = []
for label, action, plugin_result, plugin_failure, core_result, core_failure in samples:
    observations, roles, events = [], [], []
    method = {"start": "start_torrents", "pause": "stop_torrents"}.get(action, "remove_torrents")
    def plugin(*args, **kwargs):
        observations.append(dict(kind="plugin", args=list(args), kwargs=kwargs))
        if plugin_failure:
            raise RuntimeError("owned plugin control failure")
        return copy.deepcopy(plugin_result)
    def core(kwargs):
        observations.append(dict(kind="core", kwargs=kwargs))
        if core_failure:
            raise RuntimeError("owned core control failure")
        return core_result
    class Qbittorrent:
        def get_priority(self): return 0
        def get_name(self): return "Qbittorrent"
        def start_torrents(self, hashs: Union[list, str], downloader: Optional[str] = None):
            return core(dict(hashs=hashs, downloader=downloader))
        def stop_torrents(self, hashs: Union[list, str], downloader: Optional[str] = None):
            return core(dict(hashs=hashs, downloader=downloader))
        def remove_torrents(self, hashs: Union[list, str], delete_file: bool = True, downloader: Optional[str] = None):
            return core(dict(hashs=hashs, delete_file=delete_file, downloader=downloader))
    c = ns["ChainBase"]()
    c.pluginmanager = SimpleNamespace(get_plugin_modules=lambda: {
        ("OwnedModuleOverride", "Owned module override"): {method: plugin}})
    c.modulemanager = SimpleNamespace(get_running_modules=lambda method: [Qbittorrent()])
    c.messagehelper = SimpleNamespace(put=lambda **kw: roles.append(kw["role"]))
    c.eventmanager = SimpleNamespace(send_event=lambda kind, data: events.append({k:v for k,v in data.items() if k not in ("traceback", "error")}))
    kwargs = dict(hashs=["0123456789012345678901234567890123456789"], downloader="Owned downloader")
    if method == "remove_torrents":
        kwargs["delete_file"] = action == "remove_with_data"
    result = getattr(c, method)(**kwargs)
    cases.append(dict(name=label, action=action, method=method,
                      plugin_result=clean(plugin_result), plugin_failure=plugin_failure,
                      core_result=core_result, core_failure=core_failure,
                      observations=observations, roles=roles, errors=events,
                      returned=clean(result), success=bool(result)))
out = dict(commit="8a83efeacd3e26b82be495e17d50146e31c96a60",
           kind="actual fixed ChainBase control wrappers/dispatcher/ObjectUtils signature check; owned plugin and native-core outputs, API bool truth derived from fixed download endpoints",
           source_sha256={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in [path, object_path, root/'app/api/endpoints/download.py']},
           cases=cases)
Path(__file__).with_name("downloader-control-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2)+"\n")
print("fixed downloader control cases", len(cases))
