"""Execute the fixed callers with actual bytes, not a truthy JSON dictionary.

Only the transport rendering uses the Go SDK reserved bytes envelope. The
actual Python values remain bytes throughout the pinned dispatcher/callers.
"""
import asyncio
import base64
import copy
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
batch = runpy.run_path(str(here / "generate_batch_oracle.py"))
base, ns = batch["base"], batch["ns"]
batch_class, browse_class = ns["SearchChain"], base["Chain"]
title = runpy.run_path(str(here / "generate_filter_oracle.py"))
title_class, title_ns = title["ns"]["SearchChain"], title["ns"]


def wire(value):
    if isinstance(value, bytes):
        return {"__plugin_module_bytes": base64.b64encode(value).decode()}
    return base["clean"](value)


cases = []
for entry in ["browse", "batch", "title"]:
    for value in [b"", b"owned"]:
        calls = []

        async def plugin(*args, **kwargs):
            calls.append(dict(kind="plugin", method=method))
            return value

        def sync_plugin(*args, **kwargs):
            calls.append(dict(kind="plugin", method=method))
            return value

        class Core:
            def get_priority(self):
                return 0

            def get_name(self):
                return "Owned native core"

            async def async_refresh_torrents(self, **kwargs):
                calls.append(dict(kind="core", method="async_refresh_torrents"))
                return copy.deepcopy([base["core_model"], base["core_model"]])

            async def async_batch_search_torrents(self, **kwargs):
                calls.append(dict(kind="core", method="async_batch_search_torrents"))
                return {1: copy.deepcopy([base["core_model"], base["core_model"]])}

            async def async_search_torrents(self, **kwargs):
                calls.append(dict(kind="core", method="async_search_torrents"))
                return copy.deepcopy([base["core_model"], base["core_model"]])

            def get_search_page_size(self, **kwargs):
                calls.append(dict(kind="core", method="get_search_page_size"))
                return 100

            def filter_torrents(self, **kwargs):
                calls.append(dict(kind="core", method="filter_torrents"))
                return kwargs["torrent_list"]

        method = {"browse": "async_refresh_torrents", "batch": "async_batch_search_torrents", "title": "filter_torrents"}[entry]
        c = {"browse": browse_class, "batch": batch_class, "title": title_class}[entry]()
        c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {method: sync_plugin if entry == "title" else plugin}})
        c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
        c.messagehelper = types.SimpleNamespace(put=lambda **kwargs: None)
        c.eventmanager = types.SimpleNamespace(send_event=lambda *args: None)
        ns["settings"].SEARCH_RESOURCE_PAGES = 1

        async def indexers():
            return [copy.deepcopy(base["site"])]

        async def indexer(domain):
            return copy.deepcopy(base["site"])

        ns["SitesHelper"] = lambda: types.SimpleNamespace(async_get_indexers=indexers, async_get_indexer=indexer)
        try:
            if entry == "browse":
                returned = asyncio.run(c.async_browse(domain="127.0.0.1", keyword=None, cat=None, page=1))
                returned = ns["project"](returned)
            elif entry == "batch":
                returned = asyncio.run(c._SearchChain__async_search_all_sites(keyword="Owned keyword", sites=[1], page=1))
            else:
                returned = c._SearchChain__filter_title_search_torrents([copy.deepcopy(title["base"]["core_model"])])
            error = None
        except Exception as exc:
            returned, error = None, type(exc).__name__
        cases.append(dict(entry=entry, name="empty_bytes" if not value else "full_bytes", plugin_result=wire(value), returned=wire(returned), error=error, observations=calls, transport="exact source bytes represented only across SDK JSON boundary by reserved base64 envelope"))

assert [(row["entry"], row["error"], sum(call["method"] == "async_search_torrents" for call in row["observations"])) for row in cases] == [("browse", None, 0), ("browse", "AttributeError", 0), ("batch", None, 1), ("batch", None, 1), ("title", None, 0), ("title", None, 0)]
out = dict(commit=base["out"]["commit"], kind="actual pinned dispatcher and ordinary browse endpoint/batch caller/title caller with actual Python bytes; registry/native core are owned boundaries", source_sha256={**batch["out"]["source_sha256"], **title["out"]["source_sha256"]}, cases=cases, adaptation="Truthy title bytes reach the original caller unchanged; native typed public Candidate projection rejects them instead of treating the envelope as a source model. Empty bytes produce an empty result and never invoke core. Both batch bytes take the fixed AttributeError legacy fallback.")
(here / "bytes-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual ordinary resource/title bytes source cases", len(cases))
