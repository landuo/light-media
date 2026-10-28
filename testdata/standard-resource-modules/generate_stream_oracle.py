"""Replay pinned ordinary resource SSE caller, its wrappers and module dispatcher.

The source indexer registry, network result, progress sink and configured page
count are owned boundaries. No original service or original tree is modified.
"""
import ast
import asyncio
import copy
import datetime
import hashlib
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
base = runpy.run_path(str(here / "generate_oracle.py"))
ns, execute = base["ns"], base["execute"]
root = base["root"]
source = (root / "app/chain/search.py").read_text()
chain = base["chain"]
names = base["names"] + ["async_worker_search_site", "async_search_torrents", "get_search_page_size"]
nodes = [copy.deepcopy(n) for n in chain.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
assert len(nodes) == len(names)
execute([ast.ClassDef(name="ChainBase", bases=[], keywords=[], body=nodes, decorator_list=[])], "fixed/chain-stream.py")
search = next(n for n in ast.parse(source).body if isinstance(n, ast.ClassDef) and n.name == "SearchChain")
methods = ["_get_search_resource_pages", "_build_search_pages", "_should_continue_search_pages", "__async_search_all_sites_stream"]
nodes = [copy.deepcopy(n) for n in search.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in methods]
assert len(nodes) == len(methods)
ns.update(datetime=datetime.datetime, settings=types.SimpleNamespace(SEARCH_RESOURCE_PAGES=1, CONF=types.SimpleNamespace(threadpool=4)), global_vars=types.SimpleNamespace(is_system_stopped=False), asyncio=asyncio, ProgressKey=types.SimpleNamespace(Search="search"))
ns["logger"].warn = lambda *a: None
ns["ProgressHelper"] = lambda key: types.SimpleNamespace(start=lambda: None, update=lambda **kw: None, end=lambda: None)
execute([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=nodes, decorator_list=[])], "fixed/search-stream.py")
site = base["site"]

async def indexers():
    return [copy.deepcopy(site)]

ns["SitesHelper"] = lambda: types.SimpleNamespace(async_get_indexers=indexers)
calls = []
core_models = [base["core_model"]]

class Core:
    def get_priority(self):
        return 0

    def get_name(self):
        return "Owned native indexer"

    async def async_worker_search_site(self, site, keyword=None, mtype=None, cat=None, page=0):
        calls.append(dict(kind="core", method="async_worker_search_site", kwargs=copy.deepcopy(dict(site=site, keyword=keyword, mtype=mtype, cat=cat, page=page))))
        return copy.deepcopy(core_models)

    async def async_search_torrents(self, site, keyword=None, mtype=None, page=0):
        calls.append(dict(kind="core", method="async_search_torrents", kwargs=copy.deepcopy(dict(site=site, keyword=keyword, mtype=mtype, page=page))))
        return copy.deepcopy(core_models)

    def get_search_page_size(self, site, keyword=None):
        calls.append(dict(kind="core", method="get_search_page_size", kwargs=copy.deepcopy(dict(site=site, keyword=keyword))))
        return 100

cases = []
for label, value in [("none", None), ("empty_list", []), ("plugin_list", [base["plugin_model"]]), ("false", False), ("zero", 0), ("empty_dict", {}), ("bad_scalar", 7), ("failed", "owned_raise")]:
    calls = []

    async def plugin(*args, **kwargs):
        calls.append(dict(kind="plugin", method="async_worker_search_site", args=args, kwargs=copy.deepcopy(kwargs)))
        if label == "failed":
            raise RuntimeError("owned failure")
        return copy.deepcopy(value)

    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"async_worker_search_site": plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)

    async def collect():
        return [frame async for frame in c._SearchChain__async_search_all_sites_stream(keyword="Owned keyword", sites=[1], page=1)]

    try:
        frames = asyncio.run(collect())
        error = None
    except Exception as exc:
        frames = None
        error = type(exc).__name__
    # Timing/progress text is intentionally omitted; retain module sequence,
    # complete source models and exact page counters/identity.
    output = None if frames is None else [{k: base["clean"](frame[k]) for k in ["type", "items", "site_id", "page", "finished", "total", "total_items"] if k in frame} for frame in frames]
    cases.append(dict(name=label, plugin_result=base["clean"](value) if label != "failed" else None, observations=base["clean"](calls), frames=output, error=error))

assert [len([r for r in case["observations"] if r["method"] == "async_search_torrents"]) for case in cases] == [0] * 8
assert [len([r for r in case["observations"] if r["method"] == "get_search_page_size"]) for case in cases] == [1, 1, 1, 1, 1, 1, 0, 1]
pagination = []
ns["settings"].SEARCH_RESOURCE_PAGES = 3
for label, value in [("full_pages", 2), ("zero_capacity", False), ("negative_capacity", -1), ("null_capacity_core_default", None), ("string_capacity", "2"), ("list_capacity", []), ("capacity_failure", "owned_raise")]:
    calls = []

    async def plugin(*args, **kwargs):
        calls.append(dict(kind="plugin", method="async_worker_search_site", args=args, kwargs=copy.deepcopy(kwargs)))
        return [copy.deepcopy(base["plugin_model"])]

    def page_size(*args, **kwargs):
        calls.append(dict(kind="plugin", method="get_search_page_size", args=args, kwargs=copy.deepcopy(kwargs)))
        if label == "capacity_failure":
            raise RuntimeError("owned capacity failure")
        return copy.deepcopy(value)

    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"async_worker_search_site": plugin, "get_search_page_size": page_size}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)
    try:
        frames = asyncio.run(collect())
        error = None
    except Exception as exc:
        frames = None
        error = type(exc).__name__
    output = None if frames is None else [{k: base["clean"](frame[k]) for k in ["type", "items", "site_id", "page", "finished", "total", "total_items"] if k in frame} for frame in frames]
    pagination.append(dict(name=label, configured_pages=3, page_size=base["clean"](value) if label != "capacity_failure" else None, observations=base["clean"](calls), frames=output, error=error))
assert [len([r for r in case["observations"] if r["kind"] == "core" and r["method"] == "async_worker_search_site"]) for case in pagination] == [3, 3, 3, 1, 1, 1, 1]
calls = []
core_models = [base["core_model"], base["core_model"]]
ns["settings"].SEARCH_RESOURCE_PAGES = 2
value, label = 3, "production_owned_core_rows"
# The same actual caller uses a controlled two-row core boundary to match the
# real Go fixture site's two HTML rows. No source HTTP/parser equivalence claim.
frames = asyncio.run(collect())
production = dict(configured_pages=2, page_size=3, core_rows=2, observations=base["clean"](calls), frames=[{k: base["clean"](frame[k]) for k in ["type", "items", "site_id", "page", "finished", "total", "total_items"] if k in frame} for frame in frames])
assert [frame["page"] for frame in production["frames"] if frame["type"] == "append"] == [1, 2]
page_config = []
for value in [None, "bad", "0", "-3", " 2 ", "1_0", "２"]:
    ns["settings"].SEARCH_RESOURCE_PAGES = value
    page_config.append(dict(value=value, result=c._get_search_resource_pages()))
page_errors = []
ns["settings"].SEARCH_RESOURCE_PAGES = 9223372036854775808123
try:
    c._build_search_pages(1)
    raise AssertionError("oversized source range unexpectedly materialized")
except OverflowError as exc:
    page_errors.append(dict(value=str(ns["settings"].SEARCH_RESOURCE_PAGES), error=type(exc).__name__))
out = dict(commit=base["out"]["commit"], kind="actual fixed ordinary SearchChain SSE site-worker caller + complete wrappers/dispatcher, actual TorrentInfo model; owned registry/network/progress boundaries", source_sha256={"app/chain/search.py": hashlib.sha256(source.encode()).hexdigest(), **base["out"]["source_sha256"]}, normalization="elapsed/progress text omitted; owned loopback port may change; native page2 maps fixed page1", cases=cases, pagination=pagination, page_config=page_config, production_pagination=production, page_builder_errors=page_errors)
(here / "stream-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual ordinary source stream cases", len(cases))
