#!/usr/bin/env python3
"""Two controlled JSON POST preparation cases; never import requests/urllib3.

Run only after coordinator/static-review approval, with Python -I -S -B.
This executes selected original AST definitions, not package/module imports,
RequestUtils/OCR entry points, URL preparation, Session construction, or HTTP.
"""
from __future__ import annotations

import __future__
import ast
import builtins
import hashlib
import json
import re
import sys
from collections import OrderedDict
from collections.abc import Iterable, Mapping, MutableMapping
from pathlib import Path
from typing import Any, Generic, Protocol, TypeVar, cast, runtime_checkable


ROOT = Path(__file__).resolve().parent
CONTROLLED_SESSION_HEADERS = {
    "User-Agent": "python-requests/2.34.2",
    "Accept-Encoding": "gzip, deflate",
    "Accept": "*/*",
    "Connection": "keep-alive",
}
CASES = (
    ("ascii", "QQ==", r'{"base64_img": "QQ=="}'),
    (
        "unicode-and-escapes-preparation-only",
        "汉字🙂\"\\\n\t",
        r'{"base64_img": "\u6c49\u5b57\ud83d\ude42\"\\\n\t"}',
    ),
)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def select_node(tree, selector):
    kind, name = selector["kind"], selector.get("name")
    if kind == "class":
        matches = [n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == name]
    elif kind == "function":
        matches = [
            n for n in tree.body
            if isinstance(n, ast.FunctionDef) and n.name == name and not n.decorator_list
        ]
    elif kind == "assignment":
        matches = [
            n for n in tree.body if isinstance(n, ast.Assign)
            and any(isinstance(t, ast.Name) and t.id == name for t in n.targets)
        ]
    else:
        cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == selector["class"])
        method = next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == selector["method"])
        if kind == "method":
            matches = [method]
        elif kind == "request-header-dict":
            matches = [
                n.value for n in ast.walk(method)
                if isinstance(n, ast.Assign) and isinstance(n.value, ast.Dict)
                and any(isinstance(t, ast.Attribute) and isinstance(t.value, ast.Name)
                        and t.value.id == "self" and t.attr == "_headers" for t in n.targets)
            ]
        elif kind == "ocr-json-dict":
            calls = [
                n for n in ast.walk(method)
                if isinstance(n, ast.Call) and isinstance(n.func, ast.Attribute)
                and n.func.attr == "post_res" and isinstance(n.func.value, ast.Call)
                and isinstance(n.func.value.func, ast.Name)
                and n.func.value.func.id == "RequestUtils"
            ]
            assert len(calls) == 1
            constructor = calls[0].func.value
            assert not constructor.args and len(constructor.keywords) == 1
            assert constructor.keywords[0].arg == "content_type"
            assert ast.literal_eval(constructor.keywords[0].value) == "application/json"
            matches = [k.value for k in calls[0].keywords if k.arg == "json"]
        else:
            raise AssertionError("unsupported selector")
    assert len(matches) == 1, selector
    return matches[0]


def ast_digest(node):
    return digest(ast.dump(node, include_attributes=False).encode("utf-8"))


def forbid_source_imports(node):
    assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node))


def main():
    assert sys.flags.isolated and sys.flags.no_site and sys.dont_write_bytecode
    forbidden = ("requests", "urllib3", "socket")
    assert not any(n == p or n.startswith(p + ".") for n in sys.modules for p in forbidden)
    binding_bytes = (ROOT / "input-bindings.json").read_bytes()
    bindings = json.loads(binding_bytes)
    assert digest(Path(__file__).read_bytes()) == bindings["capture_script_sha256"]
    trees = {}
    for key, record in bindings["files"].items():
        data = Path(record["path"]).read_bytes()
        assert digest(data) == record["sha256"], key
        trees[key] = ast.parse(data, filename=record["path"])

    # Literal-only version observation: no package metadata loader/import.
    version_values = {}
    for key in ("requests_version", "urllib3_version"):
        assignment = next(
            n for n in trees[key].body if isinstance(n, ast.Assign)
            and any(isinstance(t, ast.Name) and t.id == "__version__" for t in n.targets)
        )
        version_values[key] = ast.literal_eval(assignment.value)
    assert version_values == {"requests_version": "2.34.2", "urllib3_version": "2.7.0"}

    # Original __import__ supports CPython typing/Generic internals, not a hook.
    # Frozen source AST has no imports or __import__ calls; no open/eval/exec/
    # socket/os/io names are provided to these selected definitions.
    # super_len is preserved verbatim, but receives exact bytes only: __len__ is
    # used, and its general file/tell/seek branches are outside this capture.
    allowed_builtins = (
        "__build_class__", "__import__", "object", "type", "isinstance", "hasattr", "getattr",
        "len", "list", "dict", "tuple", "str", "bytes", "bool", "int", "max",
        "super", "NotImplemented", "ValueError", "TypeError", "AttributeError",
        "OSError", "IOError", "NotImplementedError", "AssertionError",
    )
    namespace = {
        "__builtins__": {n: getattr(builtins, n) for n in allowed_builtins},
        "__name__": "source_preparation_ast_only",
        "OrderedDict": OrderedDict, "Mapping": Mapping, "MutableMapping": MutableMapping,
        "Iterable": Iterable, "Any": Any, "Generic": Generic, "TypeVar": TypeVar,
        "Protocol": Protocol, "runtime_checkable": runtime_checkable, "cast": cast,
        "builtin_str": str, "re": re, "complexjson": json,
        "is_urllib3_1": int(version_values["urllib3_version"].split(".")[0]) == 1,
    }
    expressions = {}
    selected = []
    for record in bindings["selected_ast"]:
        key = record["file"]
        node = select_node(trees[key], record["selector"])
        assert ast_digest(node) == record["ast_sha256"], record["label"]
        forbid_source_imports(node)
        selected.append({"label": record["label"], "ast_sha256": record["ast_sha256"], "lines": record["lines"]})
        if isinstance(node, ast.Dict):
            assert all(isinstance(v, (ast.Name, ast.Constant)) for v in node.values)
            assert all(isinstance(k, ast.Constant) and isinstance(k.value, str) for k in node.keys)
            expressions[record["label"]] = node
            continue
        # Preserve the source AST, including bodies/decorators. Future
        # annotations avoid importing annotation-only requests type modules.
        module = ast.Module(body=[node], type_ignores=[])
        exec(compile(module, bindings["files"][key]["path"], "exec",
                     flags=__future__.annotations.compiler_flag, dont_inherit=True), namespace)
        if record["label"] == "SupportsItems":
            namespace["_SupportsItems"] = namespace["SupportsItems"]

    def literal_expression(label, values):
        expression = ast.Expression(body=expressions[label])
        return eval(compile(expression, "<bound-source-dict>", "eval", dont_inherit=True),
                    {"__builtins__": {}}, values)

    request_headers = literal_expression("RequestUtils._headers", {
        "ua": None, "content_type": "application/json", "accept_type": None, "referer": None,
    })
    SourcePreparedBody = type("SourcePreparedBody", (), {
        name: namespace[name] for name in ("prepare_headers", "prepare_body", "prepare_content_length")
    })
    cases = []
    for label, text, expected_ascii in CASES:
        assert type(text) is str
        payload = literal_expression("OcrHelper.post_res.json", {"image_b64": text})
        assert type(payload) is dict and list(payload) == ["base64_img"] and type(payload["base64_img"]) is str
        session_headers = namespace["CaseInsensitiveDict"](CONTROLLED_SESSION_HEADERS)
        merged = namespace["merge_setting"](request_headers, session_headers,
                                             dict_class=namespace["CaseInsensitiveDict"])
        prepared = SourcePreparedBody()
        prepared.method = "POST"
        prepared.prepare_headers(merged)
        prepared.prepare_body(data=None, files=None, json=payload)
        # Only the OCR preparation assertions requested; headers/length are
        # observed outputs, not claims about Session.send or the wire protocol.
        assert prepared.headers["Content-Type"] == "application/json"
        assert "Accept" not in prepared.headers
        assert type(prepared.body) is bytes and prepared.body == expected_ascii.encode("ascii")
        cases.append({
            "case": label, "payload": payload,
            "source_request_headers_before_merge": request_headers,
            "merged_headers": dict(merged.items()),
            "prepared_headers": dict(prepared.headers.items()),
            "body_hex": prepared.body.hex(), "body_ascii": prepared.body.decode("ascii"),
            "body_bytes": len(prepared.body), "body_sha256": digest(prepared.body),
        })
    assert not any(n == p or n.startswith(p + ".") for n in sys.modules for p in forbidden)
    output = {
        "stage": "selected-real-source-json-post-preparation-only",
        "source_commit": bindings["source_commit"], "versions_from_literal_source": version_values,
        "input_bindings_sha256": digest(binding_bytes), "capture_script_sha256": bindings["capture_script_sha256"],
        "python": {"version": sys.version, "executable": sys.executable, "isolated": True, "no_site": True},
        "json_backend": "controlled standard-library json; not a rediscovery of requests.compat backend",
        "controlled_session_headers": CONTROLLED_SESSION_HEADERS,
        "default_header_claim": "controlled input previously observed by coordinator; not a new measurement or old br environment replay",
        "http_observed": False, "full_source_environment": False, "requests_or_urllib3_imported": False,
        "url_preparation_or_session_send_executed": False,
        "unicode_case_scope": "synthetic POST preparation string; not OCR image validity or full input normalization",
        "selected_source_definitions": selected, "cases": cases,
    }
    print(json.dumps(output, ensure_ascii=True, indent=2))


if __name__ == "__main__":
    main()
