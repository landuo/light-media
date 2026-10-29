#!/usr/bin/env python3
"""Original full-module filesystem contrasts on owned review fixtures only."""
import base64
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import types

ROOT = Path(__file__).resolve().parent.parent
E = ROOT / "evidence"
HELPER = ROOT / "light-media/testdata/plugins/category-editor-1.3/installed-category-helper.py"
PLUGIN = ROOT / "source-snapshot/installed-plugin-sources/categoryeditor/__init__.py"
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT.parent / "toolchain/python-oracle/ruamel-0.19.1"))
import ruamel.yaml
assert ruamel.yaml.__version__ == "0.19.1"
assert hashlib.sha256(HELPER.read_bytes()).hexdigest() == "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2"
assert hashlib.sha256(PLUGIN.read_bytes()).hexdigest() == "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"
INITIAL = "movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n"
DEFAULT = "movie:\n  默认电影:\n"
NEW = "movie:\n  New:\n"
fixtures = Path(tempfile.mkdtemp(prefix="categoryeditor-review-filesystem-source-fixtures-", dir=E))
rows = []
for mode in ["write_only", "symlink", "missing_file", "external_change_without_init"]:
    root = fixtures / mode
    config = root / "config"
    inner = root / "inner"
    config.mkdir(parents=True)
    inner.mkdir()
    user = config / "category.yaml"
    (inner / "category.yaml").write_text(DEFAULT)
    if mode != "missing_file":
        user.write_text(INITIAL)
    logs = []
    trace = []
    for name in ["app", "app.core", "app.schemas", "app.utils", "app.modules"]:
        module = types.ModuleType(name)
        module.__path__ = []
        sys.modules[name] = module
    settings = types.SimpleNamespace(CONFIG_PATH=config, INNER_CONFIG_PATH=inner)
    logger = types.SimpleNamespace(**{level: (lambda *a, level=level, **k: logs.append({"level": level, "args": [str(x).replace(str(root), "$CASE") for x in a]})) for level in ["warn", "info", "error"]})
    for name, attrs in [("app.core.config", {"settings": settings}), ("app.log", {"logger": logger}), ("app.schemas.category", {"CategoryConfig": type("UnusedCategoryConfig", (), {})}), ("app.utils.singleton", {"WeakSingleton": type})]:
        module = types.ModuleType(name)
        module.__dict__.update(attrs)
        sys.modules[name] = module
    helper_module = types.ModuleType("archived_helper_filesystem")
    exec(compile(HELPER.read_bytes(), str(HELPER), "exec"), helper_module.__dict__)
    before_exists = user.exists()
    helper = helper_module.CategoryHelper()
    row = {"id": mode, "file_before_constructor": before_exists, "file_after_constructor": user.exists(), "initial_match": helper.get_movie_category({"original_language": "zh"})}
    if mode == "write_only":
        user.chmod(0o200)
    if mode == "symlink":
        owned = config / "owned-target.yaml"
        user.rename(owned)
        user.symlink_to(owned)
    if mode == "external_change_without_init":
        user.write_text(NEW)
        row["match_without_reloading"] = helper.get_movie_category({"original_language": "zh"})
    if mode in ["write_only", "symlink"]:
        class Base:
            def __init__(self):
                self.systemmessage = types.SimpleNamespace(put=lambda *a, **k: trace.append({"event": "put", "args": list(a), "kwargs": k}))
            def update_config(self, c):
                trace.append({"event": "update_config", "config": c})
                return True
        for name, attrs in [("app.plugins", {"_PluginBase": Base}), ("app.modules.themoviedb", {"CategoryHelper": lambda: helper})]:
            module = types.ModuleType(name)
            module.__dict__.update(attrs)
            sys.modules[name] = module
        plugin_module = types.ModuleType("original_plugin_filesystem")
        exec(compile(PLUGIN.read_bytes(), str(PLUGIN), "exec"), plugin_module.__dict__)
        plugin = plugin_module.CategoryEditor()
        try:
            row["apply_return"] = plugin.init_plugin({"enabled": True, "content": NEW})
        except Exception as exc:
            row["apply_error"] = {"type": type(exc).__name__, "message": str(exc).replace(str(root), "$CASE")}
        row["enabled"] = plugin.get_state()
        row["match_after_apply"] = helper.get_movie_category({"original_language": "zh"})
        row["trace"] = trace
    row["mode_at_observation"] = oct(user.stat().st_mode & 0o777)
    row["is_symlink"] = user.is_symlink()
    if mode == "write_only":
        # Inspection after all observed calls; restore original mode afterward.
        user.chmod(0o600)
        row["inspection_permission_change_after_observation"] = "0200 -> 0600 -> 0200, owned fixture only"
    row["actual_bytes_base64"] = base64.b64encode(user.read_bytes()).decode()
    if mode == "write_only":
        user.chmod(0o200)
    row["logs"] = logs
    rows.append(row)
    print(json.dumps(row, ensure_ascii=True))
output = {"kind": "controlled_original_filesystem_source_contrasts", "helper_current_nas_verified": False, "source_bindings": {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in [HELPER, PLUGIN, Path(__file__)]}, "fixtures": str(fixtures.relative_to(ROOT)), "initial": INITIAL, "default": DEFAULT, "applied": NEW, "cases": rows}
path = E / "categoryeditor-review-filesystem-source.json"
path.write_text(json.dumps(output, ensure_ascii=True, indent=2) + "\n")
print(json.dumps({"output_sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "cases": len(rows)}))
