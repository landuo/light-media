"""Actual fixed media filter prefix, model identity and shared MediaInfo.

The prefix ends exactly after the source's __do_parallel_filter assignment;
native title/episode matching, sorting and final Context projection are outside
this replay. Core returns its real input list as an explicitly owned boundary.
"""
import ast
import concurrent.futures
import copy
import hashlib
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
base = runpy.run_path(str(here / "generate_filter_oracle.py"))
ns, execute = base["ns"], base["execute"]
parse = copy.deepcopy(next(n for n in base["search"].body if isinstance(n, ast.FunctionDef) and n.name == "__parse_result"))
prefix = []
for node in parse.body:
    prefix.append(node)
    if isinstance(node, ast.Assign) and isinstance(node.value, ast.Call) and isinstance(node.value.func, ast.Name) and node.value.func.id == "__do_parallel_filter":
        break
else:
    raise AssertionError("fixed media filter assignment not found")
parse.body = prefix + [ast.Return(value=ast.Tuple(elts=[ast.Name(id="torrents", ctx=ast.Load()), ast.Name(id="mediainfo", ctx=ast.Load())], ctx=ast.Load()))]
execute([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=[parse], decorator_list=[])], "fixed/media-filter-prefix.py")
context_source = (base["base"]["root"] / "app/core/context.py").read_text()
model = copy.deepcopy(next(n for n in ast.parse(context_source).body if isinstance(n, ast.ClassDef) and n.name == "MediaInfo"))
execute([model], "fixed/media-info.py")
ns.update(ThreadPoolExecutor=concurrent.futures.ThreadPoolExecutor, as_completed=concurrent.futures.as_completed, ProgressKey=types.SimpleNamespace(Search="search"), ProgressHelper=lambda key: types.SimpleNamespace(start=lambda: None, update=lambda **kw: None), settings=types.SimpleNamespace(CONF=types.SimpleNamespace(threadpool=4)))
ns["logger"].warn = lambda *args: None

cases = []
for sites in [1, 2]:
    for mode in ["same_input_alias", "copy_equal", "copy_container", "false", "media_mutate_then_fail"]:
        rows = [copy.deepcopy(base["base"]["core_model"])]
        if sites == 2:
            rows.append(copy.deepcopy(rows[0]))
            rows[-1].site, rows[-1].site_name = 2, "Owned second site"
        refs = {id(row): i for i, row in enumerate(rows)}
        media = ns["MediaInfo"]()
        media.source, media.tmdb_id, media.title, media.category = "themoviedb", 900043, "Owned media", "Owned original category"
        media.names = [media.title]
        observations = []

        def plugin(*args, **kwargs):
            observations.append(dict(kind="plugin", kwargs={"rule_groups": copy.deepcopy(kwargs["rule_groups"]), "torrent_list": base["base"]["clean"](copy.deepcopy(kwargs["torrent_list"])), "mediainfo": dict(title=kwargs["mediainfo"].title, category=kwargs["mediainfo"].category)}))
            if mode == "same_input_alias":
                return kwargs["torrent_list"]
            if mode == "copy_equal":
                return copy.deepcopy(kwargs["torrent_list"])
            if mode == "copy_container":
                return list(kwargs["torrent_list"])
            if mode == "media_mutate_then_fail":
                kwargs["mediainfo"].title = "Changed before error"
                kwargs["mediainfo"].category = "Changed category"
                kwargs["torrent_list"][0].pri_order = 77
                raise RuntimeError("owned media mutation before failure")
            return False

        class Core:
            def get_priority(self):
                return 0

            def get_name(self):
                return "Owned native core"

            def filter_torrents(self, rule_groups, torrent_list, mediainfo=None):
                return torrent_list

        c = ns["SearchChain"]()
        c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"filter_torrents": plugin}})
        c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
        c.messagehelper = types.SimpleNamespace(put=lambda **kwargs: None)
        c.eventmanager = types.SimpleNamespace(send_event=lambda *args: None)
        result, modified = c._SearchChain__parse_result(rows, media, rule_groups=["owned-rule"])
        cases.append(dict(sites=sites, mode=mode, returned=base["base"]["clean"](result), input_model_refs=[refs.get(id(row)) for row in result], media_after=dict(title=modified.title, category=modified.category), observations=observations))
assert [case["input_model_refs"] for case in cases[:5]] == [[0, 0], [None, 0], [0, 0], [], [0]]
assert [case["input_model_refs"] for case in cases[5:]] == [[0, 1], [0, 1], [0, 1], [], [0, 1]]
assert all(case["media_after"]["title"] == "Changed before error" for case in cases if case["mode"] == "media_mutate_then_fail")
out = dict(commit=base["out"]["commit"], source_sha256={**base["out"]["source_sha256"], "app/core/context.py": hashlib.sha256(context_source.encode()).hexdigest()}, kind="actual pinned __parse_result prefix through parallel site filter, ChainBase dispatcher, actual TorrentInfo/MediaInfo classes; native core input-list boundary, not downstream match/sort acceptance", cases=cases, target_state="media-specific DTO/host pending; titleNone evidence cannot accept these cases")
# The concrete IPC model must retain live private provider fields; the source
# public to_dict deliberately hides them, while ordinary callbacks access the
# actual model. Both source forms are captured without an upstream request.
enum_source = base["base"]["raw"]["app/schemas/types.py"]
media_type = copy.deepcopy(next(n for n in ast.parse(enum_source).body if isinstance(n, ast.ClassDef) and n.name == "MediaType"))
execute([media_type], "fixed/media-type.py")
production = ns["MediaInfo"]()
production.source, production.tmdb_id, production.type = "themoviedb", 900043, ns["MediaType"].MOVIE
production.title, production.original_title, production.year, production.release_date = "Owned keyword", "Owned original", "2026", "2026-01-01"
production.names, production.recognize_cache_hit = ["Owned keyword"], True
production.tmdb_info = dict(id=900043, title=production.title, original_title=production.original_title, release_date=production.release_date, genres=[], credits=dict(cast=[], crew=[]), external_ids={})
public = production.to_dict()
private = dict(public)
private.update(vars(production))
if "mediaid_prefix" not in vars(production):
    private.pop("mediaid_prefix", None)
for key in ["tmdb_info", "douban_info", "bangumi_info", "anilist_info"]:
    private[key] = getattr(production, key)
out["production_model"] = dict(kind="actual fixed MediaInfo default model with explicit owned fields, computed descriptors and retained live provider data", public=base["base"]["clean"](public), module_input=base["base"]["clean"](private))
cleared = copy.deepcopy(production)
cleared.clear()
clear_private = dict(cleared.to_dict())
clear_private.update(vars(cleared))
if "mediaid_prefix" not in vars(cleared):
    clear_private.pop("mediaid_prefix", None)
out["production_clear"] = base["base"]["clean"](clear_private)
mutated_clear = copy.deepcopy(production)
mutated_clear.title, mutated_clear.category = "Changed before error", "Changed category"
mutated_clear.clear()
mutated_clear_private = dict(mutated_clear.to_dict())
mutated_clear_private.update(vars(mutated_clear))
if "mediaid_prefix" not in vars(mutated_clear):
    mutated_clear_private.pop("mediaid_prefix", None)
out["production_mutated_clear"] = base["base"]["clean"](mutated_clear_private)
production.source = None
nil_public = production.to_dict()
nil_private = dict(nil_public)
nil_private.update(vars(production))
if "mediaid_prefix" not in vars(production):
    nil_private.pop("mediaid_prefix", None)
out["production_nil_source"] = dict(public=base["base"]["clean"](nil_public), module_input=base["base"]["clean"](nil_private))
assert out["production_nil_source"]["module_input"]["source"] is None
assert out["production_nil_source"]["public"]["source"] == "themoviedb"
assert out["production_model"]["module_input"]["media_id"] is None
deduplicate = copy.deepcopy(next(n for n in base["search"].body if isinstance(n, ast.FunctionDef) and n.name == "__remove_duplicate"))
execute([ast.ClassDef(name="Dedup", bases=[], keywords=[], body=[deduplicate], decorator_list=[])], "fixed/media-dedup.py")
out["dedup_cases"] = []
for name, values in [("last_value_first_position", [("high", None), ("low", None)]), ("description_distinct", [("first", "first"), ("second", "second")]), ("source_None_string_collision", [("nil", None), ("text", "None")])]:
    contexts = []
    for tag, description in values:
        torrent = copy.deepcopy(base["base"]["core_model"])
        torrent.description = description
        contexts.append(types.SimpleNamespace(torrent_info=torrent, tag=tag))
    result = ns["Dedup"]._Dedup__remove_duplicate(contexts)
    out["dedup_cases"].append(dict(name=name, input=[dict(tag=c.tag, torrent=base["base"]["clean"](c.torrent_info)) for c in contexts], returned_tags=[c.tag for c in result]))
assert [case["returned_tags"] for case in out["dedup_cases"]] == [["low"], ["first", "second"], ["text"]]
(here / "media-filter-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual fixed media filter site/identity/mutation cases", len(cases))
