#!/usr/bin/env python3
"""Additional source-only observations requested during independent review.
Reuses the frozen whole-module source replay driver, never the target parser.
"""
import base64
import hashlib
import json
from pathlib import Path
import runpy
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
E = ROOT / "evidence"
sys.dont_write_bytecode = True
DEPS = ROOT.parent / "toolchain/python-oracle/ruamel-0.19.1"
sys.path.insert(0, str(DEPS))
import ruamel.yaml
assert ruamel.yaml.__version__ == "0.19.1"
driver = E / "categoryeditor-source-helper-replay.py"
ns = runpy.run_path(str(driver), run_name="review_source_reuse")
base = ns["inputs"]()

rows = []
def add(name, text, media=None):
    rows.append({"id": name, "mode": "file", "content_base64": base64.b64encode(text.encode()).decode(), "media": media or base["media"]})

for name, value in [("bool", "true"), ("list", "[16]"), ("mapping", "{id: 16}")]:
    add("condition_" + name, "movie:\n  Bad:\n    genre_ids: " + value + "\n")
    add("earlier_mismatch_then_" + name, "movie:\n  Bad:\n    original_language: 'xx'\n    genre_ids: " + value + "\n")
    add("earlier_rule_shields_" + name, "movie:\n  First:\n  Bad:\n    genre_ids: " + value + "\n")
add("truthy_empty_string_list_deferred", "movie:\n  Bad:\n    origin_country: 16\n", {"id": 1, "origin_country": [""]})
add("truthy_empty_string_list_string_condition", "movie:\n  First:\n    origin_country: ','\n  Fallback:\n", {"id": 1, "origin_country": [""]})
add("unicode_decimal_range", "movie:\n  ArabicRange:\n    genre_ids: '١-٣'\n  Fallback:\n", {"id": 1, "genre_ids": [2]})
add("unicode_superscript_range", "movie:\n  SuperscriptRange:\n    genre_ids: '²-³'\n  Fallback:\n", {"id": 1, "genre_ids": [2]})
add("more_than_64_categories", "movie:\n" + "".join("  Category%02d:\n" % i for i in range(65)))
add("more_than_128_extra_fields", "movie:\n  Fields:\n" + "".join("    field%03d: 'x'\n" % i for i in range(129)))
add("empty_condition_name", "movie:\n  EmptyField:\n    '': 'x'\n  Fallback:\n")
add("long_falsey_condition_name", "movie:\n  LongField:\n    '" + "x" * 129 + "': false\n")
add("control_condition_name", 'movie:\n  ControlField:\n    "x\\ny": false\n')
add("nonstring_condition_name", "movie:\n  NumericField:\n    16: 'x'\n  Fallback:\n")
add("numeric_category_name", "movie:\n  123:\n")
add("section_scalar", "movie: 5\ntv: {}\n")
add("rule_scalar", "movie:\n  ScalarRule: 5\ntv: {}\n")
add("ignored_nonstring_root_key", "true: ignored\nmovie:\n  Normal:\n")
add("ignored_timestamp_value", "extra: 2020-01-01\nmovie:\n  Normal:\n")
add("ignored_custom_tag", "extra: !custom text\nmovie:\n  Normal:\n")
add("timestamp_condition", "movie:\n  Timestamp:\n    release_date: 2020-01-01\n")
add("yaml11_boolean_condition", "%YAML 1.1\n---\nmovie:\n  LegacyOn:\n    original_language: on\n")
add("duplicate_true_and_one", "movie:\n  true:\n  1:\n")
add("unknown_root_deep", "extra:\n" + "".join("  " * (i + 1) + "n%d:\n" % i for i in range(33)) + "movie:\n  Normal:\n")
add("html_escaped_payload_under_64k", "#" + "<" * 24000 + "\nmovie:\n  Normal:\n")

input_path = E / "categoryeditor-review-source-extra-inputs.json"
input_path.write_text(json.dumps({"kind": "additional_source_only_inputs", "cases": rows}, ensure_ascii=True, indent=2) + "\n")
fixtures = Path(tempfile.mkdtemp(prefix="categoryeditor-review-source-fixtures-", dir=E))
output = {"kind": "controlled_archived_helper_additional_source_observations", "helper_date": "2026-10-20", "current_nas_helper_verified": False, "bindings": {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in [driver, ns["SOURCE"], ns["PLUGIN_SOURCE"], input_path, Path(__file__)]}, "fixtures": str(fixtures.relative_to(ROOT)), "cases": []}
for row in rows:
    data = dict(base)
    data["media"] = row["media"]
    result = ns["replay"](row, fixtures / row["id"], data)
    output["cases"].append(result)
    print(json.dumps({"id": row["id"], "init": result["warm"]["call"], "movie": result["warm"]["lookups"]["movie_match"], "plugin": result["warm"]["plugin_with_retained_archived_helper"]["call"]}, ensure_ascii=True))
path = E / "categoryeditor-review-source-extra-oracle.json"
path.write_text(json.dumps(output, ensure_ascii=True, indent=2) + "\n")
print(json.dumps({"cases": len(rows), "output_sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "scope": "source observations only"}))
