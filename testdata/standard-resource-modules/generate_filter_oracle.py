"""Pinned ordinary title filter caller + wrapper/dispatcher with owned core.

Explicitly retains source alias/mutation observations; it is not native filter
host coverage until matching existing-host implementation and evidence exist.
"""
import ast
import copy
import hashlib
import json
import pathlib
import runpy
import types

here = pathlib.Path(__file__).resolve().parent
base = runpy.run_path(str(here / "generate_oracle.py"))
ns, execute, chain = base["ns"], base["execute"], base["chain"]
names = base["names"] + ["filter_torrents"]
nodes = [copy.deepcopy(n) for n in chain.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
execute([ast.ClassDef(name="ChainBase", bases=[], keywords=[], body=nodes, decorator_list=[])], "fixed/filter-chain.py")
source = (base["root"] / "app/chain/search.py").read_text()
search = next(n for n in ast.parse(source).body if isinstance(n, ast.ClassDef) and n.name == "SearchChain")
caller = copy.deepcopy(next(n for n in search.body if isinstance(n, ast.FunctionDef) and n.name == "__filter_title_search_torrents"))
execute([ast.ClassDef(name="SearchChain", bases=[ast.Name(id="ChainBase", ctx=ast.Load())], keywords=[], body=[caller], decorator_list=[])], "fixed/filter-title.py")
ns["SystemConfigKey"] = types.SimpleNamespace(SearchFilterRuleGroups="search-rules")
ns["SystemConfigOper"] = lambda: types.SimpleNamespace(get=lambda key: ["owned-rule"])
calls = []

class Core:
    def get_priority(self):
        return 4

    def get_name(self):
        return "Owned native priority filter"

    def filter_torrents(self, rule_groups, torrent_list, mediainfo=None):
        calls.append(dict(kind="core", method="filter_torrents", kwargs=base["clean"](copy.deepcopy(dict(rule_groups=rule_groups, torrent_list=torrent_list, mediainfo=mediainfo)))))
        if label in ["alias_core_mutation", "copy_equal", "copy_container"]:
            for model in torrent_list:
                model.pri_order = 100
        return torrent_list

cases = []
for label, value in [("no_groups", None), ("none", None), ("empty_list", []), ("plugin_list", [base["plugin_model"]]), ("false", False), ("empty_dict", {}), ("bad_scalar", 7), ("same_input_alias", None), ("mutate_then_none", None), ("mutate_then_fail", None), ("alias_core_mutation", None), ("copy_equal", None), ("copy_container", None)]:
    calls = []
    original = [copy.deepcopy(base["core_model"])]

    def plugin(*args, **kwargs):
        calls.append(dict(kind="plugin", method="filter_torrents", args=base["clean"](args), kwargs=base["clean"](copy.deepcopy(kwargs))))
        if label in ["same_input_alias", "alias_core_mutation"]:
            return kwargs["torrent_list"]
        if label == "copy_equal":
            return copy.deepcopy(kwargs["torrent_list"])
        if label == "copy_container":
            return list(kwargs["torrent_list"])
        if label in ["mutate_then_none", "mutate_then_fail"]:
            kwargs["torrent_list"][0].pri_order = 77
            if label == "mutate_then_fail":
                raise RuntimeError("owned mutation before callback failure")
        return copy.deepcopy(value)

    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"filter_torrents": plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)
    try:
        result = c._SearchChain__filter_title_search_torrents(original, rule_groups=[] if label == "no_groups" else None)
        error = None
    except Exception as exc:
        result, error = None, type(exc).__name__
    cases.append(dict(name=label, plugin_result=base["clean"](value), observations=base["clean"](calls), returned=base["clean"](result), input_after=base["clean"](original), returned_is_input=result is original, error=error))
by_name = {case["name"]: case for case in cases}
assert len(by_name["same_input_alias"]["input_after"]) == 2
assert by_name["mutate_then_none"]["input_after"][0]["pri_order"] == 77
assert by_name["mutate_then_fail"]["input_after"][0]["pri_order"] == 77
assert [row["pri_order"] for row in by_name["copy_equal"]["returned"]] == [0, 100]
assert [row["pri_order"] for row in by_name["copy_container"]["returned"]] == [100, 100]
multiple = []
for first_kind in ["copy_equal", "same_input_alias"]:
    calls, label = [], "multi_handler"
    original = [copy.deepcopy(base["core_model"])]
    def first(*args, **kwargs):
        calls.append(dict(kind="plugin", method="filter_torrents", instance="source", kwargs=base["clean"](copy.deepcopy(kwargs))))
        return copy.deepcopy(kwargs["torrent_list"]) if first_kind == "copy_equal" else kwargs["torrent_list"]
    def second(*args, **kwargs):
        calls.append(dict(kind="plugin", method="filter_torrents", instance="clone", kwargs=base["clean"](copy.deepcopy(kwargs))))
        kwargs["torrent_list"][0].pri_order = 77
        raise RuntimeError("owned clone mutates before failure")
    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"filter_torrents": first}, ("OwnedModuleOverridefilter", "owned clone"): {"filter_torrents": second}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)
    result = c._SearchChain__filter_title_search_torrents(original)
    multiple.append(dict(first_kind=first_kind, observations=base["clean"](calls), returned=base["clean"](result), input_after=base["clean"](original)))
assert [[r["pri_order"] for r in case["returned"]] for case in multiple] == [[0, 77], [77, 77]]
production = []
for label in ["copy_equal", "alias_core_mutation", "mutate_then_fail"]:
    calls = []
    original = [copy.deepcopy(base["core_model"]), copy.deepcopy(base["core_model"])]
    initial_refs = {id(model): index for index, model in enumerate(original)}
    before = base["clean"](copy.deepcopy(original))
    c = ns["SearchChain"]()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {("OwnedModuleOverride", "owned"): {"filter_torrents": plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Core()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: None)
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: None)
    result = c._SearchChain__filter_title_search_torrents(original)
    production.append(dict(name=label, input_before=before, input_models_are_same=original[0] is original[1], observations=base["clean"](calls), returned=base["clean"](result), returned_input_model_refs=[initial_refs.get(id(model)) for model in result], input_after=base["clean"](original)))
assert [[row["pri_order"] for row in case["returned"]] for case in production] == [[0, 0, 100, 100], [100, 100, 100, 100], [77, 0]]
out = dict(commit=base["out"]["commit"], kind="actual fixed ordinary title-filter caller/wrapper/dispatcher and actual TorrentInfo models; core response identity/mutation is owned boundary; no rule parser acceptance", source_sha256={"app/chain/search.py": hashlib.sha256(source.encode()).hexdigest(), **base["out"]["source_sha256"]}, cases=cases, multi_handlers=multiple, production=production, target_state="explicit title-specific Go model/list refs implement per-call aliases/input mutation; native host/source SDK evidence tracked separately, no Python heap framework")
(here / "filter-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual ordinary title-filter source cases", len(cases))
