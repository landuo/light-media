"""Pinned media-aware sync/async site callers and prepare_params algorithm.

Media attributes used by these selected callers and the native network result
are owned inputs, not a claim that the entire source MediaInfo provider ran.
"""
import ast
import asyncio
import concurrent.futures
import copy
import json
import pathlib
import runpy
import types
from enum import Enum

here = pathlib.Path(__file__).resolve().parent
batch = runpy.run_path(str(here / "generate_batch_oracle.py"))
base, ns = batch["base"], batch["ns"]
chain = base["chain"]
names = batch["stream"]["names"] + ["search_torrents"]
nodes = [copy.deepcopy(n) for n in chain.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
base["execute"]([ast.ClassDef(name="ChainBase", bases=[], keywords=[], body=nodes, decorator_list=[])], "fixed/media-chain.py")
names = ["__prepare_params", "_get_search_resource_pages", "_build_search_pages", "_should_continue_search_pages", "__search_all_sites", "__async_search_all_sites", "__async_search_all_sites_stream"]
nodes = [copy.deepcopy(n) for n in batch["search"].body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
assert len(nodes) == len(names)
ns.update(ThreadPoolExecutor=concurrent.futures.ThreadPoolExecutor, wait=concurrent.futures.wait, FIRST_COMPLETED=concurrent.futures.FIRST_COMPLETED)
base["execute"]([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=nodes, decorator_list=[])], "fixed/media-search.py")
ns["SitesHelper"] = lambda: types.SimpleNamespace(get_indexers=lambda: [copy.deepcopy(base["site"])], async_get_indexers=batch["stream"]["indexers"])
ns["settings"].SEARCH_RESOURCE_PAGES = 1

class MediaType(Enum):
    MOVIE = "电影"
    TV = "电视剧"

calls = []
value = None

class Core(batch["Core"]):
    def batch_search_torrents(self, sites, keyword=None, mtype=None, page=0):
        kwargs = dict(sites=sites, keyword=keyword, mtype=mtype, page=page)
        calls.append(dict(kind="core", method="batch_search_torrents", kwargs=copy.deepcopy(kwargs)))
        return {1: copy.deepcopy([base["core_model"]])}

    def search_torrents(self, site, keyword=None, mtype=None, page=0):
        kwargs = dict(site=site, keyword=keyword, mtype=mtype, page=page)
        calls.append(dict(kind="core", method="search_torrents", kwargs=copy.deepcopy(kwargs)))
        return copy.deepcopy([base["core_model"]])

    async def async_search_torrents(self, site, keyword=None, mtype=None, page=0):
        kwargs = dict(site=site, keyword=keyword, mtype=mtype, page=page)
        calls.append(dict(kind="core", method="async_search_torrents", kwargs=copy.deepcopy(kwargs)))
        return copy.deepcopy([base["core_model"]])

    async def async_worker_search_site(self, site, keyword=None, mtype=None, cat=None, page=0):
        kwargs = dict(site=site, keyword=keyword, mtype=mtype, cat=cat, page=page)
        calls.append(dict(kind="core", method="async_worker_search_site", kwargs=copy.deepcopy(kwargs)))
        return copy.deepcopy([base["core_model"]])

    def get_search_page_size(self, site, keyword=None):
        kwargs = dict(site=site, keyword=keyword)
        calls.append(dict(kind="core", method="get_search_page_size", kwargs=copy.deepcopy(kwargs)))
        return 100


cases = []
for entry, kind, area, value in [("sync", MediaType.MOVIE, "title", {1: [base["plugin_model"]]}), ("sync", MediaType.TV, "imdbid", False), ("async", MediaType.MOVIE, "title", {1: [base["plugin_model"]]}), ("async", MediaType.MOVIE, "imdbid", False), ("stream", MediaType.MOVIE, "title", [base["plugin_model"]])]:
    calls = []
    method = {"sync": "batch_search_torrents", "async": "async_batch_search_torrents", "stream": "async_worker_search_site"}[entry]

    def plugin(*args, **kwargs):
        calls.append(dict(kind="plugin", method=method, kwargs=copy.deepcopy(kwargs)))
        return copy.deepcopy(value)

    media = types.SimpleNamespace(type=kind, imdb_id="tt1234567")
    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {method: plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kwargs: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *args: None)
    if entry == "sync":
        returned = c._SearchChain__search_all_sites(keyword="Owned title", mediainfo=media, sites=[1], page=0, area=area)
    elif entry == "async":
        returned = asyncio.run(c._SearchChain__async_search_all_sites(keyword="Owned title", mediainfo=media, sites=[1], page=0, area=area))
    else:
        async def collect():
            frames = [frame async for frame in c._SearchChain__async_search_all_sites_stream(keyword="Owned title", mediainfo=media, sites=[1], page=0, area=area)]
            return [row for frame in frames for row in frame.get("items", [])]
        returned = asyncio.run(collect())
    cases.append(dict(entry=entry, media_type=kind.value, area=area, keyword="Owned title", actual_keyword="tt1234567" if area == "imdbid" else "Owned title", plugin_result=base["clean"](value), observations=base["clean"](calls), returned=base["clean"](returned)))

plans = []
for names, keyword, limit in [(["Owned", "Original", "English", "HongKong", "Taiwan", "Singapore"], None, 3), (["Owned", "Owned", "", None, "Taiwan", "Singapore"], None, 0), (["Owned", "Original", "English", "HongKong", "Taiwan", "Singapore"], None, -2), (["Owned", "Original", "English", "HongKong", "Taiwan", "Singapore"], "  exact user input  ", 3)]:
    ns["settings"].MAX_SEARCH_NAME_LIMIT = limit
    media = types.SimpleNamespace(**dict(zip(["title", "original_title", "en_title", "hk_title", "tw_title", "sg_title"], names)), source="themoviedb", tmdb_id=900043, season=None)
    ns["resolve_media_identity"] = lambda media: ("themoviedb", 900043)
    ns["build_media_key"] = lambda source, mid: "themoviedb:900043"
    scope, keywords = c._SearchChain__prepare_params(mediainfo=media, keyword=keyword)
    plans.append(dict(fields=vars(media), keyword=keyword, maximum_names=limit, keywords=keywords, season_episodes=scope))
assert [row["keywords"] for row in plans] == [["Owned", "Original", "English"], ["Owned", "Taiwan", "Singapore"], ["Owned", "Original", "English", "HongKong"], ["  exact user input  "]]
out = dict(commit=base["out"]["commit"], source_sha256=batch["out"]["source_sha256"], kind="actual pinned ordinary media sync/async/stream site callers + prepare_params; owned media attributes/indexer/native network result", cases=cases, keyword_plans=plans)
(here / "media-search-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual fixed media-aware search callers", len(cases), "keyword plans", len(plans))
