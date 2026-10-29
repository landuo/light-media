#!/usr/bin/env python3
"""Sanity-check recorded source observations against direct source contracts.
Not a target test and not a full-host equivalence verdict.
"""
import base64
import hashlib
import json
from pathlib import Path

E = Path(__file__).resolve().parent
core = json.loads((E / "categoryeditor-source-oracle.json").read_text())
helper = json.loads((E / "categoryeditor-source-helper-oracle.json").read_text())
inputs = json.loads((E / "categoryeditor-source-inputs.json").read_text())
C = {c["id"]: c["observations"] for c in core["cases"]}
H = {c["id"]: c for c in helper["cases"]}
checks = []


def check(name, value):
    checks.append({"id": name, "ok": bool(value)})


def members(step):
    return step["after"]["members"]


def en(step):
    return members(step)["enabled"]


def calls(step):
    return [x["event"] for x in step["events"]]


def exception(step, name):
    return step.get("exception", {}).get("type") == name


for name, observations in C.items():
    if name.startswith("falsey_config_"):
        check(name + ".unchanged", observations[0]["before"] == observations[0]["after"] and calls(observations[0]) == [])
    if name.startswith("retained_falsey_config_"):
        check(name + ".retains_failed_state", en(observations[1]) == {"type": "str", "value": "keep-enabled"} and observations[1]["before"] == observations[1]["after"] and observations[3]["before"] == observations[3]["after"])
    if name.startswith("enabled_failure_"):
        value = observations[0]["input"]["config"]["enabled"]
        check(name + ".raw_type_retained", exception(observations[0], "RuntimeError") and en(observations[0])["value"] == value and observations[1]["return"] == en(observations[0]) and observations[2]["before"] == observations[2]["after"])

check("missing_enabled_is_none", en(C["enabled_missing"][0]) == {"type": "NoneType", "value": None})
check("null_enabled_is_none", en(C["enabled_null"][0]) == {"type": "NoneType", "value": None})
check("false_is_bool_not_none", en(C["enabled_false"][0]) == {"type": "bool", "value": False})
normal = ["path.write_text.begin", "path.write_text.return", "CategoryHelper.construct", "CategoryHelper.init", "systemmessage.put", "update_config"]
for name in ["enabled_true", "enabled_one", "enabled_negative", "enabled_fraction", "enabled_false_string", "enabled_list_nonempty", "enabled_object_nonempty", "helper_init_false", "message_false", "update_false", "update_none"]:
    step = C[name][0]
    check(name + ".full_order_ignores_return_false", calls(step) == normal and "exception" not in step and en(step) == {"type": "bool", "value": False})
    check(name + ".message_before_off_and_update_after_off", step["events"][-2]["members"]["enabled"]["value"] == step["input"]["config"]["enabled"] and step["events"][-1]["members"]["enabled"] == {"type": "bool", "value": False})

for name in ["missing", "null", "object", "false", "zero", "zero_float", "string", "list"]:
    step = C["content_" + name][0]
    check("content_" + name + ".empty_file_and_content", members(step)["content"] == {"type": "str", "value": ""} and step["after"]["user_file"]["bytes"] == 0)
for name in ["integer", "list_nonempty", "object_nonempty"]:
    step = C["content_" + name][0]
    check("content_" + name + ".type_error_no_file_change", exception(step, "TypeError") and step["before"]["user_file"] == step["after"]["user_file"] and len(calls(step)) == 2)
step = C["content_surrogate"][0]
check("surrogate_truncates_before_unicode_error", exception(step, "UnicodeEncodeError") and step["after"]["user_file"]["bytes"] == 0 and en(step)["value"] is True)
for name in ["true", "integer", "string", "list"]:
    step = C["nonmapping_config_" + name][0]
    check("nonmapping_" + name + ".attribute_error_no_mutation", exception(step, "AttributeError") and step["before"] == step["after"])
for name, stage in [("helper_constructor_raise", "CategoryHelper.construct"), ("helper_init_raise", "CategoryHelper.init"), ("message_raise", "systemmessage.put"), ("update_raise", "update_config")]:
    step = C[name][0]
    check(name + ".partial_order", exception(step, "RuntimeError") and calls(step)[-1] == stage and en(step)["value"] is (False if name == "update_raise" else True))
    check(name + ".stop_no_effect", C[name][2]["before"] == C[name][2]["after"] and calls(C[name][2]) == [])
for name, error in [("missing_parent", "FileNotFoundError"), ("directory", "IsADirectoryError"), ("read_only", "PermissionError")]:
    step = C["write_failure_" + name][0]
    check(name + ".actual_os_write_failure", exception(step, error) and calls(step) == ["path.write_text.begin", "path.write_text.error"] and en(step)["value"] is True)
check("duplicate_apply_not_deduped_by_plugin", calls(C["duplicate_successful_apply"][0]) == normal and calls(C["duplicate_successful_apply"][1]) == normal and len(C["duplicate_successful_apply"][1]["after"]["notifications"]) == 2)
for step in [C["form_reads_bundled_not_user_or_member"][1], C["form_reads_bundled_not_user_or_member"][3]]:
    check("form_uses_bundled_" + str(step["step"]), step["return"]["type"] == "tuple" and step["return"]["value"][1] == {"enabled": False, "content": inputs["default_text"].replace("\r\n", "\n")} and step["before"] == step["after"])
for name, error in [("missing", "FileNotFoundError"), ("directory", "IsADirectoryError"), ("invalid_utf8", "UnicodeDecodeError"), ("read_only", "PermissionError")]:
    check("form_" + name + ".actual_read_failure", exception(C["form_default_" + name][0], error))
check("fresh_object_defaults_not_retained_failure", en(C["fresh_object_after_failure"][1]) == {"type": "bool", "value": False} and not members(C["fresh_object_after_failure"][1])["enabled_is_instance_member"])
check("import_paths_not_dynamic_settings", C["import_paths_frozen"][1]["after"]["user_file"]["bytes"] > 0 and "exception" not in C["import_paths_frozen"][2])

for name in ["blank", "comments", "null", "empty_map", "empty_sequence", "false", "zero", "empty_string", "parser_failure", "duplicate_key", "multiple_documents"]:
    item = H[name]
    check("helper_" + name + ".warm_retains_cold_empty", item["warm"]["lookups"]["movie_match"]["return"]["value"] == "缓存电影" and item["cold"]["lookups"]["movie_match"]["return"]["value"] == "")
for temperature in ["warm", "cold"]:
    item = H["numeric_exact"][temperature]
    chain = item["plugin_with_retained_archived_helper"]
    check("numeric_" + temperature + ".delayed_failure", item["call"]["return"]["value"] is None and item["lookups"]["movie_categorys"]["return"]["value"] == [{"type": "str", "value": "Numeric"}] and item["lookups"]["empty_media"]["return"]["value"] == "" and item["lookups"]["movie_match"]["exception"]["type"] == "AttributeError" and "split" in item["lookups"]["movie_match"]["exception"]["message"])
    check("numeric_" + temperature + ".plugin_applied_before_match_error", chain["call"]["return"]["value"] is None and chain["enabled"]["value"] is False and [x["event"] for x in chain["trace"]] == ["CategoryHelper.factory_existing_object", "systemmessage.put", "update_config"])
    for name, match in [("large_comment_65k", "合法分类"), ("path_like_category", "../escape"), ("cycle_bounded", "Recursive")]:
        item = H[name][temperature]
        check(name + "." + temperature + ".raw_source_accepts", item["lookups"]["movie_match"]["return"]["value"] == match and item["plugin_with_retained_archived_helper"]["enabled"]["value"] is False)
check("cycle_child_completed_with_bound", H["cycle_bounded"]["bounded_process"]["exit_code"] == 0 and H["cycle_bounded"]["bounded_process"]["timeout_seconds"] == 8)
check("large_comment_exceeds_64k", H["large_comment_65k"]["warm"]["user_file"]["bytes"] > 65536)

out = {"kind": "source_contract_harness_sanity_only_not_target_acceptance", "binding": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in [E / "categoryeditor-source-replay.py", E / "categoryeditor-source-helper-replay.py", E / "categoryeditor-source-inputs.json", E / "categoryeditor-source-helper-inputs.json", E / "categoryeditor-source-oracle.json", E / "categoryeditor-source-helper-oracle.json"]}, "checks": checks, "failed": [x["id"] for x in checks if not x["ok"]]}
(E / "categoryeditor-source-check-results.json").write_text(json.dumps(out, ensure_ascii=True, indent=2) + "\n")
print(json.dumps({"checks": len(checks), "failed": out["failed"], "scope": out["kind"]}))
raise SystemExit(bool(out["failed"]))
