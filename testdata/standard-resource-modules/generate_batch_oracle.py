"""Pinned non-SSE search caller: batch map, AttributeError fallback, page hook.

Registry, core network list and progress sinks are controlled boundaries. This
is an execution oracle, not evidence that those native hosts are already wired.
"""
import ast
import asyncio
import copy
import hashlib
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
stream = runpy.run_path(str(here / "generate_stream_oracle.py"))
base = stream["base"]
ns, execute = stream["ns"], stream["execute"]
source = stream["source"]
search = stream["search"]
methods = ["_get_search_resource_pages", "_build_search_pages", "_should_continue_search_pages", "__async_search_all_sites"]
nodes = [copy.deepcopy(n) for n in search.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in methods]
assert len(nodes) == len(methods)
execute([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=nodes, decorator_list=[])], "fixed/search-batch.py")
ns["settings"].SEARCH_RESOURCE_PAGES = 1
calls = []
core_models = [base["core_model"]]

class Core:
    def get_priority(self):
        return 0

    def get_name(self):
        return "Owned native indexer"

    async def async_batch_search_torrents(self, sites, keyword=None, mtype=None, page=0):
        calls.append(dict(kind="core", method="async_batch_search_torrents", kwargs=copy.deepcopy(dict(sites=sites, keyword=keyword, mtype=mtype, page=page))))
        return {site["id"]: copy.deepcopy(core_models) for site in sites}

    async def async_search_torrents(self, site, keyword=None, mtype=None, page=0):
        calls.append(dict(kind="core", method="async_search_torrents", kwargs=copy.deepcopy(dict(site=site, keyword=keyword, mtype=mtype, page=page))))
        return copy.deepcopy(core_models)

    def get_search_page_size(self, site, keyword=None):
        calls.append(dict(kind="core", method="get_search_page_size", kwargs=copy.deepcopy(dict(site=site, keyword=keyword))))
        return 100

cases = []
for label, value in [("none", None), ("empty_dict", {}), ("plugin_dict", {1: [base["plugin_model"]]}), ("empty_list", []), ("plugin_list", [base["plugin_model"]]), ("false", False), ("bad_scalar", "owned bad scalar"), ("failed", "owned_raise")]:
    calls = []

    async def plugin(*args, **kwargs):
        calls.append(dict(kind="plugin", method="async_batch_search_torrents", args=args, kwargs=copy.deepcopy(kwargs)))
        if label == "failed":
            raise RuntimeError("owned batch failure")
        return copy.deepcopy(value)

    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"async_batch_search_torrents": plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)
    try:
        result = asyncio.run(c._SearchChain__async_search_all_sites(keyword="Owned keyword", sites=[1], page=1))
        error = None
    except Exception as exc:
        result, error = None, type(exc).__name__
    cases.append(dict(name=label, plugin_result=base["clean"](value) if label != "failed" else None, observations=base["clean"](calls), returned=base["clean"](result), error=error))
assert [len([r for r in case["observations"] if r["kind"] == "core" and r["method"] == "async_batch_search_torrents"]) for case in cases] == [1, 0, 0, 1, 1, 0, 0, 1]
assert [len([r for r in case["observations"] if r["method"] == "async_search_torrents"]) for case in cases] == [0, 0, 0, 1, 1, 1, 1, 0]
calls, label, value = [], "production_owned_core_rows", []
core_models = [base["core_model"], base["core_model"]]
result = asyncio.run(c._SearchChain__async_search_all_sites(keyword="Owned keyword", sites=[1], page=1))
production = dict(plugin_result=[], core_rows=2, observations=base["clean"](calls), returned=base["clean"](result))
assert len(production["returned"]) == 2
calls, label, value = [], "legacy_override", False
async def legacy_plugin(*args, **kwargs):
    calls.append(dict(kind="plugin", method="async_search_torrents", args=args, kwargs=copy.deepcopy(kwargs)))
    return [copy.deepcopy(base["plugin_model"])]
c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"async_batch_search_torrents": plugin, "async_search_torrents": legacy_plugin}})
result = asyncio.run(c._SearchChain__async_search_all_sites(keyword="Owned keyword", sites=[1], page=1))
legacy = dict(plugin_batch=False, plugin_legacy=base["clean"]([base["plugin_model"]]), core_rows=2, observations=base["clean"](calls), returned=base["clean"](result))
assert len(legacy["returned"]) == 3
out = dict(commit=base["out"]["commit"], kind="actual fixed non-SSE SearchChain async batch caller + exact fallback worker wrappers/dispatcher; actual source TorrentInfo models; owned indexer/network/progress boundaries", source_sha256={"app/chain/search.py": hashlib.sha256(source.encode()).hexdigest(), **base["out"]["source_sha256"]}, cases=cases, production_empty_list=production, legacy_override=legacy, transport_boundary="Python numeric site map keys become decimal-string JSON keys; Go SDK batch DTO must explicitly interpret those as authenticated numeric source site IDs, not arbitrary string-key equivalence", current_target_state="source oracle + incremental native implementation evidence, independent next-main gate pending")
(here / "batch-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual ordinary nonstream batch source cases", len(cases))
