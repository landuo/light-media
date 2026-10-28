"""Execute fixed SiteSpider's domain assignment/slash and URL concatenation."""
import ast
import copy
import hashlib
import json
import pathlib
import types
import urllib.parse

root = pathlib.Path("/Users/accidia/projects/MoviePilot")
path = root / "app/modules/indexer/spider/__init__.py"
text = path.read_text()
cls = next(n for n in ast.parse(text).body if isinstance(n, ast.ClassDef) and n.name == "SiteSpider")
init = next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == "__init__")
assign = next(n for n in init.body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Attribute) and t.attr == "domain" for t in n.targets))
slash = next(n for n in init.body if isinstance(n, ast.If) and "endswith" in ast.unparse(n.test))
build = next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == "__get_search_url")
url = next(n for n in ast.walk(build) if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "searchurl" for t in n.targets) and isinstance(n.value, ast.BinOp) and "self.domain" in ast.unparse(n.value))
rows = []
for domain in ["127.0.0.1", "http://127.0.0.1:43100"]:
    ns = dict(self=types.SimpleNamespace(), indexer={"domain": domain}, torrentspath="torrents.php", inputs_dict={})
    unit = ast.Module(body=copy.deepcopy([assign, slash, url]), type_ignores=[])
    exec(compile(ast.fix_missing_locations(unit), "fixed/SiteSpider-domain-and-URL.py", "exec"), ns)
    parsed = urllib.parse.urlsplit(ns["searchurl"])
    rows.append(dict(indexer_domain=domain, source_request_url=ns["searchurl"], absolute_http=parsed.scheme in ["http", "https"] and bool(parsed.netloc)))
assert rows[0]["absolute_http"] is False and rows[1]["absolute_http"] is True
out = dict(commit="8a83efeacd3e26b82be495e17d50146e31c96a60", kind="actual selected consumer statements, no compiled SitesHelper implementation claim; URL split validates absolute request shape", source_sha256=hashlib.sha256(text.encode()).hexdigest(), cases=rows)
pathlib.Path(__file__).with_name("indexer-domain-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("fixed indexer domain consumer proves HTTP base URL, DB lookup hostname is separate")
