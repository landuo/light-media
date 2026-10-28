"""Pinned process caller keyword order/stop, missing-name fill and limiter.

The per-site search callable and final match callable are explicitly owned
boundaries; the fixed complete process and MinIntervalLimiter methods execute.
"""
import ast
import copy
import hashlib
import json
import pathlib
import runpy
import threading
import types

here = pathlib.Path(__file__).resolve().parent
base = runpy.run_path(str(here / "generate_media_search_oracle.py"))
ns = base["ns"]
nodes = [copy.deepcopy(n) for n in base["batch"]["search"].body if isinstance(n, ast.FunctionDef) and n.name in ["process", "__prepare_params", "_media_recognize_kwargs"]]
base["base"]["execute"]([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=nodes, decorator_list=[])], "fixed/media-process.py")
recognition = runpy.run_path(str(here.parent / "standard-module-host/generate-recognize-oracle.py"))
ns["resolve_media_identity"] = recognition["resolve_ns"]["resolve_media_identity"]
ns["build_media_key"] = lambda source, mid: f"{source}:{mid}"
limit_path = base["base"]["root"] / "app/utils/limit.py"
limit_source = limit_path.read_text()
limiter = copy.deepcopy(next(n for n in ast.parse(limit_source).body if isinstance(n, ast.ClassDef) and n.name == "MinIntervalLimiter"))
ns["threading"] = threading
base["base"]["execute"]([limiter], "fixed/mininterval.py")
cases = []
for name, multiple, nonempty, missing_names in [("stop_on_raw_result", False, {"Owned"}, False), ("all_names_enabled", True, {"Owned"}, False), ("empty_then_result", False, {"Original"}, False), ("fill_names_once_then_empty", False, set(), True)]:
    observations = []
    tick = [100.0]
    ns["time"] = types.SimpleNamespace(monotonic=lambda: tick[0], sleep=lambda wait: tick.__setitem__(0, tick[0] + wait))
    ns["_keyword_search_limiter"] = ns["MinIntervalLimiter"](2)
    ns["settings"].MAX_SEARCH_NAME_LIMIT = 3
    ns["settings"].SEARCH_MULTIPLE_NAME = multiple
    media = types.SimpleNamespace(source="themoviedb", media_id="900043", tmdb_id=900043, douban_id=None, bangumi_id=None, anilist_id=None, type=base["MediaType"].MOVIE, title="Owned", original_title="Original", en_title="English", hk_title=None, tw_title=None, sg_title=None, names=[] if missing_names else ["present"], season=None)
    c = ns["SearchChain"]()
    def recognize(**kwargs):
        observations.append(dict(kind="recognize", kwargs=base["base"]["clean"](kwargs)))
        media.names = ["present"]
        return media
    def search(**kwargs):
        observations.append(dict(kind="search", keyword=kwargs["keyword"], clock=tick[0]))
        return [copy.deepcopy(base["base"]["plugin_model"])] if kwargs["keyword"] in nonempty else []
    c.recognize_media = recognize
    c._SearchChain__search_all_sites = search
    c._SearchChain__parse_result = lambda **kwargs: kwargs["torrents"]
    result = c.process(media)
    cases.append(dict(name=name, multiple_names=multiple, initial_names_missing=missing_names, nonempty_keywords=sorted(nonempty), observations=observations, returned=base["base"]["clean"](result), final_clock=tick[0]))
assert [[o["keyword"] for o in case["observations"] if o["kind"] == "search"] for case in cases] == [["Owned"], ["Owned", "Original", "English"], ["Owned", "Original"], ["Owned", "Original", "English"]]
assert cases[-1]["observations"][0]["kind"] == "recognize"
assert cases[-1]["observations"][0]["kwargs"]["mediaid"] == "900043"
assert [case["final_clock"] for case in cases] == [100.0, 102.0, 100.0, 102.0]
out = dict(commit=base["out"]["commit"], source_sha256={**base["out"]["source_sha256"], "app/utils/limit.py": hashlib.sha256(limit_source.encode()).hexdigest()}, kind="actual fixed SearchChain.process and MinIntervalLimiter with controlled monotonic clock, owned recognition/site search/final match boundaries", cases=cases)
(here / "media-query-sequence-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual fixed media process sequence cases", len(cases))
