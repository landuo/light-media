"""Capture pinned original clone orchestration with owned temporary dependencies.

Executes the exact original clone_plugin method. Storage, registration and code
rewriting are boundary doubles, not the original full application. All filesystem
copy/cleanup performed by the original method stays inside TemporaryDirectory.
No original configuration, network, media path, scheduler or credential is used.
"""
import ast
import copy
import hashlib
import json
import tempfile
import textwrap
from pathlib import Path
from types import SimpleNamespace
from typing import Tuple

HERE = Path(__file__).resolve().parent
SOURCE = HERE / "reference/clone_plugin.py"
text = SOURCE.read_text()
node = ast.parse(textwrap.dedent(text)).body[0]
assert isinstance(node, ast.FunctionDef) and node.name == "clone_plugin"


def capture(name, suffix, config, *, duplicate=False, rewrite_ok=True):
    with tempfile.TemporaryDirectory(prefix="light-media-clone-oracle-") as temporary:
        root = Path(temporary)
        original = root / "app/plugins/systemprocess"
        original.mkdir(parents=True)
        (original / "__init__.py").write_text("# owned synthetic plugin bytes\n")
        state = {"installed": ["SystemProcess"], "writes": {}, "reloads": [], "inits": []}
        class ConfigStore:
            def get(self, key): return list(state["installed"])
            def set(self, key, value): state["installed"] = list(value)
        class Manager:
            _plugins = {"SystemProcess": object()}
            _running_plugins = {}
            def is_plugin_exists(self, target): return duplicate
            def _modify_plugin_files(self, **kwargs): return rewrite_ok, "synthetic rewrite refused" if not rewrite_ok else "ok"
            def get_plugin_config(self, target): return copy.deepcopy(config if target == "SystemProcess" else state["writes"].get(target))
            def save_plugin_config(self, target, value, force=False): state["writes"][target] = copy.deepcopy(value)
            def reload_plugin(self, target):
                state["reloads"].append(target)
                self._running_plugins[target] = SimpleNamespace(init_plugin=lambda value: state["inits"].append(copy.deepcopy(value)))
        manager = Manager()
        namespace = dict(Tuple=Tuple, Path=Path, settings=SimpleNamespace(ROOT_PATH=root),
                         SystemConfigOper=ConfigStore, SystemConfigKey=SimpleNamespace(UserInstalledPlugins="installed"),
                         PluginManager=lambda: manager, logger=SimpleNamespace(info=lambda *a: None, error=lambda *a: None))
        exec(compile(ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[])), str(SOURCE), "exec"), namespace)
        ok, result = namespace["clone_plugin"](manager, "SystemProcess", suffix, "合成分身", "", "view-8", "")
        target = "SystemProcess" + suffix.lower()
        return dict(name=name, suffix=suffix, config=config, success=ok, result=result,
                    clone_config=state["writes"].get(target), reloads=state["reloads"], init_count=len(state["inits"]),
                    copied_directory=(root / "app/plugins" / target.lower()).exists())

cases = [capture("copied_flags_and_unknown_values", "ABC", {"enabled": True, "enable": True, "onlyonce": True, "zero": 0, "flag": False, "nested": {"empty": ""}}),
         capture("absent_config", "Empty", None),
         capture("single_flag_original_adds_second_spelling", "Single", {"enable": True}),
         capture("duplicate", "Dup", {}, duplicate=True),
         capture("rewrite_failure_cleans_owned_copy", "Fail", {}, rewrite_ok=False)]
print(json.dumps(dict(source_commit="8a83efeacd3e26b82be495e17d50146e31c96a60",
                      source_blob="8fc8954d7e7df32b970f16f461efa244fa2f142c", source_path="app/core/plugin.py",
                      source_lines=[1905,2003], excerpt_sha256=hashlib.sha256(SOURCE.read_bytes()).hexdigest(),
                      method="exact original AST method; synthetic config/registration/code-rewrite boundaries; real stdlib copy in owned temp",
                      cases=cases), ensure_ascii=False, indent=2))
