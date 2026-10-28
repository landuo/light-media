"""Pinned SSE yields the fast site before a blocked slow site's response."""
import asyncio
import copy
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
base = runpy.run_path(str(here / "generate_media_search_oracle.py"))
ns = base["ns"]
site1 = copy.deepcopy(base["base"]["site"])
site2 = copy.deepcopy(site1)
site1["name"], site2["id"], site2["name"] = "slow", 2, "fast"
site2["domain"], site2["url"] = "http://127.0.0.1:43101", "http://127.0.0.1:43101"

async def indexers():
    return [site1, site2]

ns["SitesHelper"] = lambda: types.SimpleNamespace(async_get_indexers=indexers)
ns["settings"].SEARCH_RESOURCE_PAGES = 1

async def run():
    slow_started, release = asyncio.Event(), asyncio.Event()
    calls = []
    class Core:
        def get_priority(self):
            return 0
        def get_name(self):
            return "Owned real network boundary"
        async def async_worker_search_site(self, site, keyword=None, mtype=None, cat=None, page=0):
            calls.append(dict(method="async_worker_search_site", site=site["id"]))
            if site["id"] == 1:
                slow_started.set()
                await release.wait()
            else:
                await slow_started.wait()
            model = copy.deepcopy(base["base"]["core_model"])
            model.site, model.site_name = site["id"], site["name"]
            return [model]
        def get_search_page_size(self, site, keyword=None):
            calls.append(dict(method="get_search_page_size", site=site["id"]))
            return 100
    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kwargs: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *args: None)
    media = types.SimpleNamespace(type=base["MediaType"].MOVIE, imdb_id=None)
    iterator = c._SearchChain__async_search_all_sites_stream(keyword="Owned keyword", mediainfo=media, sites=[1, 2])
    first = None
    async for frame in iterator:
        if frame["type"] == "append":
            first = dict(site_id=frame["site_id"], count=len(frame["items"]), finished=frame["finished"], total=frame["total"], slow_released=release.is_set())
            release.set()
            break
    rest = [frame async for frame in iterator]
    return dict(first_append=first, remaining_append_sites=[frame["site_id"] for frame in rest if frame["type"] == "append"], calls=calls)

result = asyncio.run(run())
assert result["first_append"] == dict(site_id=2, count=1, finished=1, total=2, slow_released=False)
assert result["remaining_append_sites"] == [1]
out = dict(commit=base["out"]["commit"], source_sha256=base["out"]["source_sha256"], kind="actual pinned media SSE site caller/wrappers/dispatcher; owned two native network responses coordinated by events", result=result)

async def run_pages():
    release=asyncio.Event()
    class Core:
        def get_priority(self): return 0
        def get_name(self): return "Owned two page boundary"
        async def async_worker_search_site(self,site,keyword=None,mtype=None,cat=None,page=0):
            if page==1: await release.wait()
            model=copy.deepcopy(base["base"]["core_model"])
            model.title=f"Owned page {page}"
            return [model]
        def get_search_page_size(self,site,keyword=None): return 1
    c=ns['SearchChain']();c.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{})
    c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[Core()]);c.messagehelper=types.SimpleNamespace(put=lambda **kwargs:None);c.eventmanager=types.SimpleNamespace(send_event=lambda *args:None)
    ns['settings'].SEARCH_RESOURCE_PAGES=2
    iterator=c._SearchChain__async_search_all_sites_stream(keyword='Owned keyword',mediainfo=types.SimpleNamespace(type=base['MediaType'].MOVIE,imdb_id=None),sites=[1])
    first=None
    async for frame in iterator:
        if frame['type']=='append':
            first=dict(page=frame['page'],count=len(frame['items']),total=frame['total'],next_page_released=release.is_set());release.set();break
    rest=[frame async for frame in iterator]
    return dict(first_append=first,remaining_pages=[frame['page'] for frame in rest if frame['type']=='append'])
out['pages']=asyncio.run(run_pages())
assert out['pages']['first_append']==dict(page=0,count=1,total=2,next_page_released=False)
assert out['pages']['remaining_pages']==[1]
(here / "media-stream-timing-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual source first append while slow blocked", result["first_append"])
