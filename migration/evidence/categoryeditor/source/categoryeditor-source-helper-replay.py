#!/usr/bin/env python3
"""Replay archived 2026-10-20 CategoryHelper with real ruamel 0.19.1 and files.

Not a certification of current NAS helper. WeakSingleton is a controlled ordinary
metaclass: this checks explicit warm/cold objects, not real singleton lifecycle.
"""
import argparse
import base64
import datetime
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import types

ROOT = Path(__file__).resolve().parent.parent
EVIDENCE = ROOT / "evidence"
SOURCE = ROOT / "light-media/testdata/plugins/category-editor-1.3/installed-category-helper.py"
PLUGIN_SOURCE = ROOT / "source-snapshot/installed-plugin-sources/categoryeditor/__init__.py"
PLUGIN_SHA = "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"
SHA = "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2"
PROVENANCE = SOURCE.parent / "reload-yaml-provenance.json"
INITIAL = "movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n"
DEFAULT = "movie:\n  内置电影:\ntv:\n  内置剧集:\n"
MEDIA = {"original_language": "zh", "origin_country": ["CN"], "genre_ids": [16], "production_countries": [{"iso_3166_1": "CN"}], "release_date": "2020-02-01"}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def encode(value, parents=None):
    parents = set() if parents is None else parents
    result = {"type": type(value).__name__}
    if isinstance(value, (dict, list, tuple)):
        if id(value) in parents:
            result["value"] = "<cycle>"
            return result
        parents = parents | {id(value)}
    if isinstance(value, dict):
        result["entries"] = [[encode(k, parents), encode(v, parents)] for k, v in value.items()]
    elif isinstance(value, (list, tuple)):
        result["value"] = [encode(v, parents) for v in value]
    elif value is None or isinstance(value, (str, bool, int, float)):
        result["value"] = value
    elif isinstance(value, (datetime.date, datetime.datetime)):
        result["value"] = value.isoformat()
    else:
        result["repr"] = repr(value)
    return result


def inputs():
    values = [
        ("blank", ""), ("comments", "# cleared policy\n"), ("null", "null\n"),
        ("empty_map", "{}\n"), ("empty_sequence", "[]\n"), ("false", "false\n"),
        ("zero", "0\n"), ("empty_string", "''\n"),
        ("parser_failure", "movie:\n  [ordinary syntax error\n"),
        ("duplicate_key", "movie: {}\nmovie: {}\n"),
        ("multiple_documents", "movie: {}\n---\ntv: {}\n"),
        ("scalar_true", "true\n"), ("scalar_integer", "42\n"),
        ("scalar_string", "'text'\n"), ("nonempty_sequence", "[a, b]\n"),
        ("movie_only", "movie:\n  新电影:\n"), ("tv_only", "tv:\n  新剧集:\n"),
        ("both_empty", "movie: {}\ntv: {}\n"), ("unknown_only", "other: x\n"),
        ("movie_scalar", "movie: 5\ntv: {}\n"),
        ("condition_integer", "movie:\n  数字条件:\n    genre_ids: 16\n"),
        ("condition_false", "movie:\n  假值条件:\n    genre_ids: false\n"),
        ("ordered_and_invert", "movie:\n  非华语:\n    original_language: '!zh'\n  中文动画:\n    original_language: 'zh,en'\n    genre_ids: '16'\n  后备:\ntv:\n  国内:\n    origin_country: 'CN,HK'\n  后备:\n"),
        ("range_and_countries", "movie:\n  区域年代:\n    release_year: '2019-2021'\n    production_countries: 'CN'\n  后备:\n"),
        ("no_trim", "movie:\n  带空格:\n    original_language: ' zh'\n  后备:\n"),
        ("malformed_country_items", "movie:\n  国家:\n    production_countries: 'CN'\n"),
        ("numeric_exact", "movie:\n  Numeric:\n    genre_ids: 16\n"),
        ("large_comment_65k", "#" + "a" * (65 * 1024) + "\nmovie:\n  合法分类:\ntv: {}\n"),
        ("path_like_category", "movie:\n  '../escape':\ntv: {}\n"),
        ("cycle_bounded", "movie: &m\n  Recursive: *m\ntv: {}\n"),
    ]
    cases = [{"id": name, "content_base64": base64.b64encode(text.encode()).decode(), "mode": "file"} for name, text in values]
    cases += [{"id": "invalid_utf8_replacement", "content_base64": base64.b64encode(b"movie:\n  \xff:\n").decode(), "mode": "file"}]
    cases += [{"id": mode, "mode": mode} for mode in ["missing_user_copy_default", "missing_parent_copy_fails", "directory_read_fails", "permission_read_fails"]]
    return {"archived_helper_date": "2026-10-20", "current_nas_identity": "not verified", "initial_text": INITIAL, "default_text": DEFAULT, "media": MEDIA, "cases": cases}


def replay(case, root, data):
    results = {}
    for temperature in ["warm", "cold"]:
        current = root / temperature
        config = current / "config"
        inner = current / "inner"
        config.mkdir(parents=True)
        inner.mkdir(parents=True)
        user = config / "category.yaml"
        (inner / "category.yaml").write_bytes(data["default_text"].encode())
        logs = []

        def log(level):
            return lambda *args, **kwargs: logs.append({"level": level, "args": [str(x).replace(str(root), "$CASE") for x in args], "kwargs": kwargs})

        for name in ["app", "app.core", "app.schemas", "app.utils"]:
            module = types.ModuleType(name)
            module.__path__ = []
            sys.modules[name] = module
        for name, attrs in [
            ("app.core.config", {"settings": types.SimpleNamespace(CONFIG_PATH=config, INNER_CONFIG_PATH=inner)}),
            ("app.log", {"logger": types.SimpleNamespace(warn=log("warn"), info=log("info"), error=log("error"))}),
            ("app.schemas.category", {"CategoryConfig": type("UnusedCategoryConfigAdapter", (), {})}),
            ("app.utils.singleton", {"WeakSingleton": type}),
        ]:
            module = types.ModuleType(name)
            module.__dict__.update(attrs)
            sys.modules[name] = module
        module = types.ModuleType("archived_category_helper")
        module.__file__ = str(SOURCE)
        exec(compile(SOURCE.read_bytes(), str(SOURCE), "exec"), module.__dict__)
        cls = module.CategoryHelper

        def capture(call):
            try:
                return {"return": encode(call())}
            except Exception as exc:
                return {"exception": {"type": type(exc).__name__, "message": str(exc).replace(str(root), "$CASE")}}

        obj = cls.__new__(cls)
        initial = None
        if temperature == "warm":
            user.write_bytes(data["initial_text"].encode())
            initial = capture(lambda: cls.__init__(obj))
        mode = case["mode"]
        if mode == "file":
            user.write_bytes(base64.b64decode(case["content_base64"]))
        elif mode in ["missing_user_copy_default", "missing_parent_copy_fails"]:
            config = current / "not-present-config"
            if mode == "missing_user_copy_default":
                config.mkdir()
            user = config / "category.yaml"
        elif mode == "directory_read_fails":
            config = current / "directory-config"
            user = config / "category.yaml"
            user.mkdir(parents=True)
        elif mode == "permission_read_fails":
            user.write_text("movie: {}\n")
            user.chmod(0o000)
        sys.modules["app.core.config"].settings.CONFIG_PATH = config
        if temperature == "warm":
            obj._category_path = user
            call = capture(obj.init)
        else:
            call = capture(lambda: cls.__init__(obj))
        lookups = {}
        for name in ["is_movie_category", "is_tv_category", "movie_categorys", "tv_categorys"]:
            lookups[name] = capture(lambda name=name: getattr(obj, name))
        media = dict(data["media"])
        if case["id"] == "malformed_country_items":
            media["production_countries"] = ["CN"]
        lookups["movie_match"] = capture(lambda: obj.get_movie_category(media))
        lookups["tv_match"] = capture(lambda: obj.get_tv_category(media))
        lookups["empty_media"] = capture(lambda: obj.get_movie_category({}))
        file_data = {"exists": user.exists(), "is_directory": user.is_dir()}
        if user.is_file():
            try:
                raw = user.read_bytes()
                file_data.update(base64=base64.b64encode(raw).decode(), bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest())
            except Exception as exc:
                file_data["read_error"] = type(exc).__name__
        results[temperature] = {"initial_constructor": initial, "call": call, "members": {name: encode(getattr(obj, name)) for name in ["_categorys", "_movie_categorys", "_tv_categorys"]}, "lookups": lookups, "logs": list(logs), "user_file": file_data}
        if mode == "file":
            # Exercise the full current plugin against this retained original
            # helper object; factory injection deliberately makes no WeakSingleton
            # or current-NAS-host-lifecycle claim.
            trace = []
            class Base:
                def __init__(self):
                    self.systemmessage = types.SimpleNamespace(put=lambda *a, **k: trace.append({"event": "systemmessage.put", "args": list(a), "kwargs": k, "enabled_at_call": encode(self._enabled)}))
                def update_config(self, value):
                    trace.append({"event": "update_config", "config": value, "enabled_at_call": encode(self._enabled)})
                    return True
            def factory():
                trace.append({"event": "CategoryHelper.factory_existing_object"})
                return obj
            for name in ["app.modules"]:
                dep = types.ModuleType(name)
                dep.__path__ = []
                sys.modules[name] = dep
            for name, attrs in [("app.plugins", {"_PluginBase": Base}), ("app.modules.themoviedb", {"CategoryHelper": factory})]:
                dep = types.ModuleType(name)
                dep.__dict__.update(attrs)
                sys.modules[name] = dep
            plugin_module = types.ModuleType("current_plugin_archived_helper")
            exec(compile(PLUGIN_SOURCE.read_bytes(), str(PLUGIN_SOURCE), "exec"), plugin_module.__dict__)
            plugin = plugin_module.CategoryEditor()
            raw = base64.b64decode(case["content_base64"])
            # Plugin accepts text, so invalid UTF-8 bytes cannot be carried by
            # content; replacement is explicit, not hidden input normalization.
            content = raw.decode("utf-8", errors="replace")
            applied = capture(lambda: plugin.init_plugin({"enabled": True, "content": content}))
            results[temperature]["plugin_with_retained_archived_helper"] = {"input_content": content, "call": applied, "enabled": encode(plugin._enabled), "trace": trace, "lookups_after_apply": {"movie_categories": capture(lambda: obj.movie_categorys), "movie_match": capture(lambda: obj.get_movie_category(media)), "empty_media": capture(lambda: obj.get_movie_category({}))}, "user_file_after_base64": base64.b64encode(user.read_bytes()).decode()}
    return {"id": case["id"], **results}


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--dependency-root", type=Path, required=True)
    p.add_argument("--cycle-child-root", type=Path)
    args = p.parse_args()
    assert digest(SOURCE) == SHA
    assert digest(PLUGIN_SOURCE) == PLUGIN_SHA
    reference = json.loads(PROVENANCE.read_text())
    hashes = {name: digest(args.dependency_root / name) for name in reference["python_sources"]}
    assert hashes == reference["python_sources"], "ruamel sources differ from archive"
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(args.dependency_root))
    import ruamel.yaml
    assert ruamel.yaml.__version__ == "0.19.1"
    data = inputs()
    if args.cycle_child_root is not None:
        assert args.cycle_child_root.resolve().is_relative_to(EVIDENCE)
        result = replay(next(x for x in data["cases"] if x["id"] == "cycle_bounded"), args.cycle_child_root, data)
        print(json.dumps(result, ensure_ascii=True))
        return
    input_path = EVIDENCE / "categoryeditor-source-helper-inputs.json"
    input_path.write_text(json.dumps(data, ensure_ascii=True, indent=2) + "\n")
    fixtures = Path(tempfile.mkdtemp(prefix="categoryeditor-source-helper-fixtures-", dir=EVIDENCE))
    output = {"kind": "controlled_archived_helper_source_replay", "archived_at": "2026-10-20", "current_nas_helper_verified": False, "source_sha256": SHA, "driver_sha256": digest(Path(__file__)), "inputs_sha256": digest(input_path), "yaml_python_sources": hashes, "yaml_version": ruamel.yaml.__version__, "environment": {"python": sys.version, "platform": platform.platform()}, "isolation": {"fixtures": str(fixtures.relative_to(ROOT)), "real_file_io": True, "real_yaml": True, "source": "whole unmodified helper module; explicit __new__/__init__ retains object fields even on constructor failure", "WeakSingleton": "controlled type adapter; singleton lifecycle not tested", "CategoryConfig": "unused declaration adapter; load/save not exercised", "logger": "record-only local adapter", "production_access": False}, "cases": []}
    for case in data["cases"]:
        if case["id"] == "cycle_bounded":
            command = [sys.executable, str(Path(__file__).resolve()), "--dependency-root", str(args.dependency_root.resolve()), "--cycle-child-root", str(fixtures / case["id"])]
            try:
                run = subprocess.run(command, text=True, capture_output=True, timeout=8, check=False)
                if run.returncode != 0:
                    raise RuntimeError("cycle child exit %s: %s" % (run.returncode, run.stderr))
                result = json.loads(run.stdout)
                result["bounded_process"] = {"timeout_seconds": 8, "exit_code": run.returncode, "stderr": run.stderr}
            except subprocess.TimeoutExpired:
                result = {"id": case["id"], "bounded_process": {"timeout_seconds": 8, "status": "timed_out_not_complete"}}
        else:
            result = replay(case, fixtures / case["id"], data)
        output["cases"].append(result)
        print(json.dumps({"case": case["id"], "warm": result.get("warm", {}).get("lookups", {}).get("movie_match"), "cold": result.get("cold", {}).get("lookups", {}).get("movie_match"), "bounded_process": result.get("bounded_process")}, ensure_ascii=True))
    path = EVIDENCE / "categoryeditor-source-helper-oracle.json"
    path.write_text(json.dumps(output, ensure_ascii=True, indent=2) + "\n")
    print(json.dumps({"cases": len(output["cases"]), "output_sha256": digest(path), "status": "recorded_not_current_nas_or_target_verdict"}))


if __name__ == "__main__":
    main()
