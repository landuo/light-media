#!/usr/bin/env python3
"""Controlled replay of the exact NAS CategoryEditor source, not an MP instance.

Writes only evidence/categoryeditor-source-* artifacts and owned fixture dirs.
The full original module is compiled and executed unchanged. settings, _PluginBase,
CategoryHelper, and systemmessage are explicit controlled test adapters. Path IO
is real local IO; the Path subclass records calls without replacing IO semantics.
No target code is imported or used to calculate results.
"""
import argparse
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import platform
import sys
import tempfile
import types

SOURCE_SHA = "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"
ROOT = Path(__file__).resolve().parent.parent
EVIDENCE = ROOT / "evidence"
SOURCE = ROOT / "source-snapshot/installed-plugin-sources/categoryeditor/__init__.py"
DEFAULT = "# synthetic bundled default\r\nmovie:\r\n  默认电影:\r\ntv:\r\n  默认剧集:\r\n"
CONTENT = "movie:\n  中文😀:\n    original_language: 'zh'\ntv:\n  剧集:\n"


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def typed(value):
    return {"type": type(value).__name__, "value": copy.deepcopy(value)}


def make_inputs():
    cases = []

    def case(name, steps, **extra):
        cases.append({"id": name, "steps": steps, **extra})

    def init(value, **fault):
        return {"op": "init", "config": value, "fault": fault}

    case("fresh_getters_and_noops", [{"op": x} for x in ["get_state", "get_command", "get_api", "get_page", "stop_service", "get_state"]])
    falsies = [("null", None), ("object", {}), ("false", False), ("zero", 0), ("zero_float", 0.0), ("string", ""), ("list", [])]
    for name, value in falsies:
        case("falsey_config_" + name, [init(value), {"op": "get_state"}])
        case("retained_falsey_config_" + name, [init({"enabled": "keep-enabled", "content": "keep-content"}, helper_init="raise"), init(value), {"op": "get_state"}, {"op": "stop_service"}, {"op": "get_state"}])
    enabled_values = [("missing", None)] + [(n, v) for n, v in falsies] + [("true", True), ("one", 1), ("negative", -2), ("fraction", 0.5), ("false_string", "false"), ("list_nonempty", [False]), ("object_nonempty", {"x": False})]
    for name, value in enabled_values:
        config = {"content": CONTENT, "unrelated": {"preserve": 1}}
        if name != "missing":
            config["enabled"] = value
        case("enabled_" + name, [init(config), {"op": "get_state"}])
        if value:
            case("enabled_failure_" + name, [init(config, helper_init="raise"), {"op": "get_state"}, init(None), {"op": "get_state"}])
    for name, value in [("missing", None)] + falsies + [("unicode", CONTENT), ("line_endings", "a\r\nb\rc\n"), ("bom", "\ufeffmovie: {}\n"), ("integer", 1), ("list_nonempty", ["x"]), ("object_nonempty", {"x": 1}), ("surrogate", "a\ud800b")]:
        config = {"enabled": True}
        if name != "missing":
            config["content"] = value
        case("content_" + name, [init(config), {"op": "get_state"}])
    for name, value in [("true", True), ("integer", 1), ("string", "x"), ("list", [1])]:
        case("nonmapping_config_" + name, [init(value), {"op": "get_state"}])
    for name, fault in [
        ("helper_constructor_raise", {"helper_constructor": "raise"}),
        ("helper_init_raise", {"helper_init": "raise"}),
        ("helper_init_false", {"helper_init": "false"}),
        ("message_raise", {"message": "raise"}),
        ("message_false", {"message": "false"}),
        ("update_raise", {"update": "raise"}),
        ("update_false", {"update": "false"}),
        ("update_none", {"update": "none"}),
    ]:
        case(name, [init({"enabled": True, "content": CONTENT, "extra": "not-in-update-argument"}, **fault), {"op": "get_state"}, {"op": "stop_service"}, {"op": "get_state"}])
    for mode in ["missing_parent", "directory", "read_only"]:
        case("write_failure_" + mode, [init({"enabled": True, "content": CONTENT}), {"op": "get_state"}], user_path_mode=mode)
    case("duplicate_successful_apply", [init({"enabled": True, "content": CONTENT}), init({"enabled": True, "content": CONTENT}), {"op": "get_state"}])
    case("disabled_replaces_members_not_file", [init({"enabled": True, "content": CONTENT}), init({"enabled": False, "content": "disabled draft"}), {"op": "get_state"}])
    case("failure_then_repair", [init({"enabled": True, "content": "first"}, message="raise"), init({"enabled": True, "content": "second"}), {"op": "get_state"}])
    case("fresh_object_after_failure", [init({"enabled": True, "content": CONTENT}, helper_init="raise"), {"op": "new_object"}, init({}), {"op": "get_state"}])
    case("form_reads_bundled_not_user_or_member", [init({"enabled": True, "content": CONTENT}), {"op": "get_form"}, init({"enabled": False, "content": "draft"}), {"op": "get_form"}])
    for mode in ["missing", "directory", "invalid_utf8", "read_only"]:
        case("form_default_" + mode, [{"op": "get_form"}, {"op": "get_state"}], default_path_mode=mode)
    case("form_rereads_default", [{"op": "get_form"}, {"op": "set_default_bytes", "base64": base64.b64encode(b"# changed\r\nmovie: {}\r\n").decode()}, {"op": "get_form"}])
    case("import_paths_frozen", [{"op": "change_settings_paths"}, init({"enabled": True, "content": CONTENT}), {"op": "get_form"}])
    return {"format_version": 1, "kind": "controlled_source_replay_inputs", "default_text": DEFAULT, "initial_user_text": "# owned initial user policy\nmovie: {}\n", "cases": cases}


class ControlledReplay:
    def __init__(self, case, root, inputs):
        self.case = case
        self.root = root
        self.events = []
        self.notifications = []
        self.updates = []
        self.adapter_store = None
        self.fault = {}
        self.plugin = None
        self.user = root / "config/category.yaml"
        self.default = root / "inner/category.yaml"
        self.user.parent.mkdir(parents=True)
        self.default.parent.mkdir(parents=True)
        self.user.write_text(inputs["initial_user_text"], encoding="utf-8")
        self.default.write_bytes(inputs["default_text"].encode("utf-8"))
        mode = case.get("user_path_mode")
        if mode == "missing_parent":
            self.user = root / "missing-parent/category.yaml"
        elif mode == "directory":
            self.user = root / "user-is-directory/category.yaml"
            self.user.mkdir(parents=True)
        elif mode == "read_only":
            self.user.chmod(0o400)
        mode = case.get("default_path_mode")
        if mode == "missing":
            self.default = root / "missing-default/category.yaml"
        elif mode == "directory":
            self.default = root / "default-is-directory/category.yaml"
            self.default.mkdir(parents=True)
        elif mode == "invalid_utf8":
            self.default.write_bytes(b"\xff\xfe\x00invalid\n")
        elif mode == "read_only":
            self.default.chmod(0o000)

        owner = self
        class ObservedPath(type(Path())):
            def write_text(path, data, *args, **kwargs):
                owner.event("path.write_text.begin", path=owner.path_name(path), data=typed(data), args=list(args), kwargs=kwargs)
                try:
                    result = super().write_text(data, *args, **kwargs)
                except Exception as exc:
                    owner.event("path.write_text.error", error=owner.error(exc))
                    raise
                owner.event("path.write_text.return", result=typed(result))
                return result

            def read_text(path, *args, **kwargs):
                owner.event("path.read_text.begin", path=owner.path_name(path), args=list(args), kwargs=kwargs)
                try:
                    result = super().read_text(*args, **kwargs)
                except Exception as exc:
                    owner.event("path.read_text.error", error=owner.error(exc))
                    raise
                owner.event("path.read_text.return", result=typed(result))
                return result

        class ControlledMessage:
            def put(_, *args, **kwargs):
                value = {"args": list(args), "kwargs": kwargs}
                owner.event("systemmessage.put", **value)
                if owner.fault.get("message") == "raise":
                    raise RuntimeError("controlled systemmessage failure")
                owner.notifications.append(copy.deepcopy(value))
                return False if owner.fault.get("message") == "false" else None

        class ControlledBase:
            def __init__(self):
                self.systemmessage = ControlledMessage()

            def update_config(self, value):
                owner.updates.append(copy.deepcopy(value))
                owner.event("update_config", config=copy.deepcopy(value))
                mode = owner.fault.get("update")
                if mode == "raise":
                    raise RuntimeError("controlled update_config failure")
                if mode == "false":
                    return False
                if mode == "none":
                    return None
                owner.adapter_store = copy.deepcopy(value)
                return True

        class ControlledHelper:
            def __init__(self):
                owner.event("CategoryHelper.construct")
                if owner.fault.get("helper_constructor") == "raise":
                    raise RuntimeError("controlled CategoryHelper constructor failure")

            def init(self):
                owner.event("CategoryHelper.init")
                if owner.fault.get("helper_init") == "raise":
                    raise RuntimeError("controlled CategoryHelper init failure")
                return False if owner.fault.get("helper_init") == "false" else None

        self.settings = types.SimpleNamespace(CONFIG_PATH=ObservedPath(self.user.parent), INNER_CONFIG_PATH=ObservedPath(self.default.parent))
        for name in ["app", "app.core", "app.modules"]:
            module = types.ModuleType(name)
            module.__path__ = []
            sys.modules[name] = module
        for name, attrs in [
            ("app.core.config", {"settings": self.settings}),
            ("app.modules.themoviedb", {"CategoryHelper": ControlledHelper}),
            ("app.plugins", {"_PluginBase": ControlledBase}),
        ]:
            module = types.ModuleType(name)
            module.__dict__.update(attrs)
            sys.modules[name] = module
        module = types.ModuleType("controlled_categoryeditor_" + case["id"])
        module.__file__ = str(SOURCE)
        exec(compile(SOURCE.read_bytes(), str(SOURCE), "exec"), module.__dict__)
        self.plugin_class = module.CategoryEditor
        self.plugin = self.plugin_class()

    def path_name(self, path):
        try:
            return "$CASE/" + str(Path(path).relative_to(self.root))
        except ValueError:
            return str(path)

    def error(self, exc):
        return {"type": type(exc).__name__, "message": str(exc).replace(str(self.root), "$CASE")}

    def file_state(self, path):
        path = Path(path)
        result = {"path": self.path_name(path)}
        if not path.exists():
            result["kind"] = "missing"
        elif path.is_dir():
            result["kind"] = "directory"
        else:
            result["kind"] = "file"
            result["mode"] = oct(path.stat().st_mode & 0o777)
            try:
                value = path.read_bytes()
                result.update(bytes=len(value), base64=base64.b64encode(value).decode(), sha256=hashlib.sha256(value).hexdigest())
            except Exception as exc:
                result["read_error"] = self.error(exc)
        return result

    def members(self):
        if self.plugin is None:
            return None
        return {"enabled": typed(self.plugin._enabled), "content": typed(self.plugin._content), "enabled_is_instance_member": "_enabled" in vars(self.plugin), "content_is_instance_member": "_content" in vars(self.plugin)}

    def event(self, name, **detail):
        self.events.append({"event": name, "members": self.members(), "user_file": self.file_state(self.user), **copy.deepcopy(detail)})

    def snapshot(self):
        return {"members": self.members(), "user_file": self.file_state(self.user), "default_file": self.file_state(self.default), "notifications": copy.deepcopy(self.notifications), "update_arguments": copy.deepcopy(self.updates), "controlled_adapter_store_not_original_db": copy.deepcopy(self.adapter_store)}

    def run(self):
        results = []
        for number, step in enumerate(self.case["steps"]):
            self.fault = step.get("fault", {})
            row = {"step": number, "input": step, "before": self.snapshot()}
            start = len(self.events)
            try:
                op = step["op"]
                if op == "init":
                    value = self.plugin.init_plugin(copy.deepcopy(step["config"]))
                elif op == "new_object":
                    self.plugin = self.plugin_class()
                    value = None
                elif op == "set_default_bytes":
                    value = Path(self.default).write_bytes(base64.b64decode(step["base64"]))
                elif op == "change_settings_paths":
                    self.settings.CONFIG_PATH = self.root / "different-config"
                    self.settings.INNER_CONFIG_PATH = self.root / "different-inner"
                    value = None
                else:
                    value = getattr(self.plugin, op)()
                row["return"] = typed(value)
            except Exception as exc:
                row["exception"] = self.error(exc)
            row["events"] = copy.deepcopy(self.events[start:])
            row["after"] = self.snapshot()
            results.append(row)
        return {"id": self.case["id"], "observations": results}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--inputs", type=Path, default=EVIDENCE / "categoryeditor-source-inputs.json")
    parser.add_argument("--output", type=Path, default=EVIDENCE / "categoryeditor-source-oracle.json")
    parser.add_argument("--generate-inputs", action="store_true")
    args = parser.parse_args()
    for path in [args.inputs, args.output]:
        if path.resolve().parent != EVIDENCE or not path.name.startswith("categoryeditor-source-"):
            parser.error("artifact must be evidence/categoryeditor-source-*")
    assert sha(SOURCE) == SOURCE_SHA, "pinned source changed"
    if args.generate_inputs:
        args.inputs.write_text(json.dumps(make_inputs(), ensure_ascii=True, indent=2) + "\n")
    inputs = json.loads(args.inputs.read_text())
    fixture_root = Path(tempfile.mkdtemp(prefix="categoryeditor-source-fixtures-", dir=EVIDENCE))
    output = {
        "kind": "controlled_source_replay_not_original_host_e2e",
        "source": {"path": str(SOURCE.relative_to(ROOT)), "sha256": SOURCE_SHA, "loaded": "full unmodified module compiled in isolated process; no AST method extraction"},
        "driver_sha256": sha(Path(__file__)),
        "inputs_sha256": sha(args.inputs),
        "environment": {"python": sys.version, "implementation": platform.python_implementation(), "platform": platform.platform(), "uid": os.getuid(), "source_python_version": "unknown for current NAS snapshot; local interpreter explicitly recorded", "third_party_dependencies": []},
        "isolation": {"owned_fixtures": str(fixture_root.relative_to(ROOT)), "real_file_io": True, "production_services_started": False, "network": "no network code or endpoints", "adapters": {"settings": "owned local directories", "_PluginBase": "controlled constructor and update_config recorder; no original DB behavior asserted", "CategoryHelper": "controlled constructor/init recorder and injected faults; no YAML behavior asserted", "systemmessage": "in-process recorder; no external delivery"}, "normalization": "only owned case-directory prefix becomes $CASE in exceptions and paths; exact bytes and types retained", "fixtures_retained": True},
        "cases": [],
    }
    for case in inputs["cases"]:
        result = ControlledReplay(case, fixture_root / case["id"], inputs).run()
        output["cases"].append(result)
        errors = [r["exception"]["type"] for r in result["observations"] if "exception" in r]
        print(json.dumps({"case": case["id"], "steps": len(result["observations"]), "observed_exceptions": errors}, ensure_ascii=True))
    args.output.write_text(json.dumps(output, ensure_ascii=True, indent=2) + "\n")
    print(json.dumps({"status": "observations_recorded_not_equivalence_verdict", "cases": len(output["cases"]), "steps": sum(len(c["observations"]) for c in output["cases"]), "output": str(args.output.relative_to(ROOT)), "output_sha256": sha(args.output), "fixtures": str(fixture_root.relative_to(ROOT))}))


if __name__ == "__main__":
    main()
