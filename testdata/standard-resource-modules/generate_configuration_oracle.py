"""Actual pinned SEARCH_RESOURCE_PAGES Settings normalization and int field.

The field plus actual generic converter/validator/update_env_config are compiled;
the environment is an owned mapping containing only this synthetic field, so
the source's env-present guard prevents writes. Other settings are unverified.
"""
import ast
import copy
import hashlib
import json
import pathlib
import pydantic
import types
from typing import Any, Type, Tuple, Union, get_origin, get_args

assert pydantic.__version__ == "2.13.4"
source = pathlib.Path("/Users/accidia/projects/MoviePilot/app/core/config.py").read_text()
tree = ast.parse(source)
model = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "ConfigModel")
field = copy.deepcopy(next(n for n in model.body if isinstance(n, ast.AnnAssign) and isinstance(n.target, ast.Name) and n.target.id == "SEARCH_RESOURCE_PAGES"))
settings = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "Settings")
names = ["generic_type_converter", "generic_type_validator", "update_env_config"]
methods = [copy.deepcopy(n) for n in settings.body if isinstance(n, ast.FunctionDef) and n.name in names]
assert len(methods) == 3
diagnostics = []
ns = dict(BaseModel=pydantic.BaseModel, model_validator=pydantic.model_validator, copy=copy, json=json, Any=Any, Type=Type, Tuple=Tuple, Union=Union, get_origin=get_origin, get_args=get_args, os=types.SimpleNamespace(environ={"SEARCH_RESOURCE_PAGES": "owned-present"}), logger=types.SimpleNamespace(error=lambda *a: diagnostics.append("ERROR"), warning=lambda *a: diagnostics.append("WARN"), info=lambda *a: diagnostics.append("INFO")))
unit = ast.Module(body=[ast.ClassDef(name="Settings", bases=[ast.Name(id="BaseModel", ctx=ast.Load())], keywords=[], body=[field, *methods], decorator_list=[])], type_ignores=[])
exec(compile(ast.fix_missing_locations(unit), "fixed/ordinary-search-config-field.py", "exec"), ns)
samples = []
for raw in ["0", "-3", " 2 ", "2.0", "2.", "1_0", "1e0", "bad", "", "２", "9223372036854775808123", "\x1c2\x1c"]:
    diagnostics.clear()
    try:
        value = ns["Settings"](SEARCH_RESOURCE_PAGES=raw).SEARCH_RESOURCE_PAGES
        samples.append(dict(raw=raw, accepted=True, integer=str(value), pages=str(max(value, 1)), normalized="WARN" in diagnostics, diagnostics=diagnostics.copy()))
    except pydantic.ValidationError:
        samples.append(dict(raw=raw, accepted=False, integer=None, pages=None))
out = dict(commit="8a83efeacd3e26b82be495e17d50146e31c96a60", kind="actual selected ConfigModel int/default + actual Settings generic converter/validator/update_env_config under dev Pydantic2.13.4; owned env-present/log sink boundaries, no source file/service changed; not full original application", source_sha256=hashlib.sha256(source.encode()).hexdigest(), pydantic=pydantic.__version__, default=ns["Settings"]().SEARCH_RESOURCE_PAGES, inputs=samples)
bare = pydantic.create_model("BareFieldComparison", SEARCH_RESOURCE_PAGES=(int, 1))
comparison = []
for raw in ["2.0", "bad", "２"]:
    try:
        value = str(bare(SEARCH_RESOURCE_PAGES=raw).SEARCH_RESOURCE_PAGES)
    except pydantic.ValidationError:
        value = None
    actual = next(sample for sample in samples if sample["raw"] == raw)["integer"]
    comparison.append(dict(raw=raw, bare_pydantic=value, actual_settings=actual))
out["rejected_intermediate_assumption"] = dict(reason="Bare Pydantic field is not the actual Settings caller; converter/validator must run first. Intermediate native fatal/coercion code was corrected before any main artifact/gate.", counterexamples=comparison)
pathlib.Path(__file__).with_name("configuration-source-oracle.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
print("actual selected SEARCH_RESOURCE_PAGES source field", len(samples))
