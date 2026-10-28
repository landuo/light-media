"""Execute only pinned market.py definitions; Python is development tooling.

Usage: python3 generate_wiki_oracle.py MoviePilot [downloaded-public-plugin.md]
The optional live document is evidence input, never a runtime dependency.
"""
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlparse

COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
raw = subprocess.check_output(["git", "-C", sys.argv[1], "show", COMMIT + ":app/helper/market.py"])
tree = ast.parse(raw)
tree.body = [node for node in tree.body if isinstance(node, (ast.Assign, ast.FunctionDef))]
tree.body.insert(0, ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0))
namespace = {"re": re, "urlparse": urlparse}
exec(compile(ast.fix_missing_locations(tree), "app/helper/market.py", "exec"), namespace)
cases = []
for document in [
    "https://github.com/Outside/Ignore\n<!-- plugin-market-repos:start -->\nhttps://github.com/Owner/A.git\nhttps://github.com/owner/a/\nhttps://github.com/Owner/B\n<!-- plugin-market-repos:end -->",
    "https://github.com/Owner/A\nhttps://github.com/Owner/B.git/",
    "<!-- plugin-market-repos:end -->\nhttps://github.com/Owner/A\n<!-- plugin-market-repos:start -->",
    "<!-- plugin-market-repos:start -->\n<!-- plugin-market-repos:start -->\nhttps://github.com/Owner/A\n<!-- plugin-market-repos:end -->",
    "no repository links",
]:
    cases.append({"document": document, "repositories": namespace["extract_plugin_market_repos_from_wiki"](document)})
out = {"source_commit": COMMIT, "source": "app/helper/market.py", "source_sha256": hashlib.sha256(raw).hexdigest(), "cases": cases}
if len(sys.argv) > 2:
    document = Path(sys.argv[2]).read_bytes()
    out["live_document"] = {"url": namespace["PLUGIN_MARKET_WIKI_URL"], "sha256": hashlib.sha256(document).hexdigest(), "repositories": namespace["extract_plugin_market_repos_from_wiki"](document.decode())}
Path(__file__).with_name("wiki-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
