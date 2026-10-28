"""Development-only oracle: execute methods from the fixed MoviePilot Git tree.

The application and Go tests consume only market-oracle.json; Python is not a
runtime dependency. Usage: python3 this_file.py /path/to/MoviePilot
"""
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import textwrap
from types import SimpleNamespace

COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"


def source(path):
    return subprocess.check_output(["git", "-C", sys.argv[1], "show", f"{COMMIT}:{path}"])


def method(raw, name):
    tree = ast.parse(raw)
    for item in ast.walk(tree):
        if isinstance(item, ast.FunctionDef) and item.name == name:
            return textwrap.dedent(ast.get_source_segment(raw.decode(), item))
    raise ValueError(name)


string_source = source("app/utils/string.py")
plugin_source = source("app/core/plugin.py")
ns = {"re": re, "_version_map": {"stable": -1, "rc": -2, "beta": -3, "alpha": -4}, "_other_version": -5}
exec("from __future__ import annotations\n" + method(string_source, "compare_version"), ns)
ns["StringUtils"] = SimpleNamespace(compare_version=ns["compare_version"])
ns["PluginHelper"] = SimpleNamespace(is_local_repo_url=lambda value: value.startswith("local://"))
ns["settings"] = SimpleNamespace(PLUGIN_MARKET="https://owned.invalid/a,https://owned.invalid/b,https://owned.invalid/c")
exec("from __future__ import annotations\n" + method(plugin_source, "process_plugins_list"), ns)

pairs = [
    ("1.9", "1.10"), ("1", "1.0.0"), ("2.0", "1.99.99"),
    ("1.0-rc", "1.0"), ("1.0-stable", "1.0-rc"),
    ("1.0-beta", "1.0-alpha"), ("1.0-beta.2", "1.0-beta.10"),
    ("1.0-alpha", "1.0-alpha2"), ("1.0-RC", "1.0-rc"),
    ("1.0+build.7", "1.0"), ("1.0+build.7", "1.0+build.8"),
    ("1.0001", "1.1"), ("1.0.0.1", "1.0.0"),
    ("9999999999999999999999999999999.1", "9999999999999999999999999999998.99"),
    (" vV1.2\n", "1.2"), ("", "1.0"), ("1.0", ""),
]
comparisons = []
for left, right in pairs:
    greater = ns["compare_version"](left, ">", right)
    less = ns["compare_version"](left, "<", right)
    comparisons.append({"left": left, "right": right, "comparison": 1 if greater else -1 if less else 0})

scenarios = [
    [("First", "1.9", "a"), ("Second", "3.0", "c"), ("First", "1.10", "b")],
    [("First", "2.0", "a"), ("First", "1.0", "b")],
    [("First", "1.0", "a"), ("First", "1.0", "b")],
    [("First", "1.0", "b"), ("First", "1.0", "a")],
    [("First", "1.0.0", "a"), ("First", "1", "b")],
    [("First", "1.0-alpha", "a"), ("First", "1.0-beta", "b"), ("First", "1.0", "c")],
    [("First", "1.0", "local://First?path=/owned/one"), ("First", "1.0", "a")],
    [("First", "2.0", "local://First?path=/owned/one"), ("First", "1.0", "a")],
    [("First", "1.0", "local://First?path=/owned/one"), ("First", "1.0", "local://First?path=/owned/two")],
]
merges = []
for inputs in scenarios:
    rows = [SimpleNamespace(id=i, plugin_version=v, repo_url=r if r.startswith("local://") else f"https://owned.invalid/{r}") for i, v, r in inputs]
    selected = ns["process_plugins_list"](rows, [])
    row = lambda p: {"id": p.id, "version": p.plugin_version, "repository": p.repo_url}
    # The actual Go discovery input is in configured repository order.
    markets = ns["settings"].PLUGIN_MARKET.split(",")
    rows.sort(key=lambda p: len(markets) + 1 if p.repo_url.startswith("local://") else markets.index(p.repo_url))
    merges.append({"sources": list(map(row, rows)), "selected": list(map(row, selected))})

out = {
    "source_commit": COMMIT,
    "source_sha256": {"app/utils/string.py": hashlib.sha256(string_source).hexdigest(), "app/core/plugin.py": hashlib.sha256(plugin_source).hexdigest()},
    "comparisons": comparisons,
    "merges": merges,
}
Path(__file__).with_name("market-oracle.json").write_text(json.dumps(out, indent=2, ensure_ascii=False) + "\n")
