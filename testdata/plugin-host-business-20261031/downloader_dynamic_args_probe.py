"""Read only bound installed-source syntax; no app imports/config/DB/network."""
import ast
import hashlib
import json
from pathlib import Path

expected = {
    "brushflowlanduo/__init__.py": "1dafc17709c39b0d89e572273e4207c450096254e318aa252cabdea2f79e2301",
    "seedrescue/__init__.py": "e379ec20d09fbd3bc461b1b8f51ec508d0270a63a841763cc965c39adb98d375",
    "iyuuautoseed/__init__.py": "3cde4efa9ddd36f82318bed62a6a62ab9004257d8570538f937351b65970bdb7",
}
result = []
for name, digest in expected.items():
    path = Path("/app/app/plugins") / name
    raw = path.read_bytes()
    actual = hashlib.sha256(raw).hexdigest()
    if actual != digest:
        result.append({"file": name, "sha256": actual, "error": "bound-source-hash-mismatch"})
        continue
    tree = ast.parse(raw.decode("utf-8-sig"))
    calls = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
            continue
        if node.func.attr not in {"get_torrents", "get_downloading_torrents", "change_torrent"}:
            continue
        calls.append({"line": node.lineno, "callee": ast.unparse(node.func), "args": [ast.unparse(arg) for arg in node.args], "keywords": {arg.arg or "**": ast.unparse(arg.value) for arg in node.keywords}})
    result.append({"file": name, "sha256": actual, "calls": sorted(calls, key=lambda value: value["line"])})
print(json.dumps({"classification": "read-only original-source AST arguments, no imports of app/plugins or config/DB access", "files": result}, ensure_ascii=False))
