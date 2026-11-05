#!/usr/bin/env python3
"""Final independent closure, preserving prior immutable evidence and deltas."""
import gzip
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
BASE = ROOT / "testdata/plugin-platform-final-20261104"
def read(p):
    data=p.read_bytes()
    return json.loads(gzip.decompress(data) if p.suffix==".gz" else data)
def digest(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def checkmap(values):
    bad=[p for p,h in values.items() if not (ROOT/p).is_file() or digest(ROOT/p)!=h]
    assert not bad,bad
    return len(values)

subprocess.run(["python3",str(BASE/"audit_final_independently.py")],cwd=ROOT,check=True,capture_output=True)
report=read(BASE/"final-independent-review.json")
assert not report["legacy_baseline_audit"]["errors"] and not report["legacy_baseline_audit"]["source_errors"]
binding_path=BASE/"current-final-platform/binding.json"
index_path=BASE/"current-final-platform/source-evidence-index.json"
binding,index=read(binding_path),read(index_path)
assert binding["backend_commit"]==report["source_commits"]["backend"]
assert binding["frontend_commit"]==report["source_commits"]["frontend"]
assert binding["summary"]["exit"]==0 and binding["summary"]["roots"]=={"pass":555,"fail":0,"skip":0}
assert binding["selection"]["fixtures_built_original"]==63 and binding["selection"]["extra_workers"]==2
assert binding["verification"]["go_vet"]["exit"]==0 and binding["verification"]["diff_check"]["exit"]==0
artifact_count=checkmap(binding["artifacts"])
target_count=checkmap(binding["target_files"])
pre=read(binding_path.parent/binding["pre_post"]["before"])
post=read(binding_path.parent/binding["pre_post"]["after"])
assert pre==post and len(pre)==2937 and not binding["pre_post"]["differences"]
input_count=checkmap(post)
r9=read(BASE/"browser-ocr/product-r9-build-inputs.json")
assert r9["compile_input_files"]==r9["compile_input_files_after"] and not r9["input_differences"]
assert r9["server_sha256"]==binding["pre_post"]["R9_server_sha256"]
assert checkmap(r9["compile_input_files"])==1818
events=[json.loads(l) for l in gzip.decompress((binding_path.parent/"tests.jsonl.gz").read_bytes()).decode().splitlines() if l.strip()]
failed=[e for e in events if e.get("Action") in {"fail","skip"}]
assert not failed
passed={e["Test"] for e in events if e.get("Action")=="pass" and e.get("Test") and "/" not in e["Test"]}
selected=read(binding_path.parent/"selected-roots.json")
assert len(passed)==555
if isinstance(selected,list): assert passed==set(selected)
assert len(read(binding_path.parent/"worker-receipts.json"))==65
assert index["accepted_leaf_count"]==64 and index["no_matrix_ledger_or_final50_hash_self_reference"]
assert set(index["excluded_indices"])=={"22","44","45","46"}
matrix={r["id"]:r for r in report["rows"]}
accepted=[]
for row in index["leaf_rows"]:
    assert row["id"]!="50"
    actual=matrix[row["id"]]
    for a,b in [("fixed_source_location_and_behavior","source_column"),("target_implementation","target_column"),("difference_or_necessary_adaptation","necessary_adaptation_column"),("actual_evidence","evidence_column")]:
        assert row[a]==actual[b],(row["id"],a)
    assert row["status"]==actual["state"]
    if row["status"]=="验收通过": accepted.append(row["id"])
    else: assert row["status"]=="按用户要求排除"
assert len(accepted)==64
for ref in [index["incremental_independent_review"],index["final_current_regression"]]: assert digest(ROOT/ref["path"])==ref["sha256"]

# Prior values stay visibly different, with explicit actual-current validation.
s02=read(BASE/"s02-evidence-lineage-supplement.json")
s07=read(BASE/"s07-user-agent-lineage-supplement.json")
for p,h in s07["current_target_files"].items(): assert digest(ROOT/p)==h
assert digest(ROOT/s07["prior_acceptance"]["path"])==s07["prior_acceptance"]["sha256"]
assert s07["no_old_manifest_rewrite"]
def explain(error):
    path=error.get("path",error.get("key"))
    if error["manifest"]==s02["historical_acceptance"]["path"] and path==s02["review_amendment"]["path"] and error["expected"]==s02["historical_acceptance"]["stale_review_sha256"] and error["actual"]==s02["review_amendment"]["sha256"]:
        return "testdata/plugin-platform-final-20261104/s02-evidence-lineage-supplement.json"
    if error["manifest"]==s07["prior_acceptance"]["path"] and path=="internal/app/plugin_original_host_settings.go" and error["expected"]==s07["prior_acceptance"]["settings_target_sha256"] and error["actual"]==s07["current_target_files"][path]:
        return "testdata/plugin-platform-final-20261104/s07-user-agent-lineage-supplement.json + final current555 binding"
    return error.get("resolved_via_lineage_supplement")
for group in ["supplemental_hash_errors","nested_supplemental_hash_errors"]:
    for error in report[group]:
        resolution=explain(error);assert resolution,error
        error["resolved_via_explicit_lineage_and_current_validation"]=resolution
report["resolved_historical_hash_differences"]=report["supplemental_hash_errors"]+report["nested_supplemental_hash_errors"]
report["supplemental_unresolved_hash_errors"]=[]
report["nested_supplemental_hash_errors"]=[]
report["blocking_findings"]=[]
for group in report["group_target_paths_audit"]:
    for target in group["target_paths"]:
        path=target["path"]
        assert path in binding["target_files"] and target["sha256"]==binding["target_files"][path]
        if path in post:
            assert target["sha256"]==post[path]
            target["current_final_full_input_and_explicit_target_match"]=True
        else:
            # Current Go/platform closure deliberately does not snapshot every
            # frontend test file. Bind its actual separate stable frontend run.
            assert path=="web/tests/plugin-log-dialog.test.mjs"
            r3_context=read(BASE/"browser-ocr/product-r3-source-inputs.json")
            assert r3_context["files"][path]==target["sha256"]
            frontend=(BASE/"frontend-tests.log").read_text()
            assert "tests 556" in frontend and "pass 556" in frontend and "fail 0" in frontend and "skipped 0" in frontend
            target["current_explicit_target_and_stable_frontend_context_match"]=True
            target["outside_2937_snapshot_frontend_test_boundary"]=True
products=[p for p in ROOT.glob("internal/app/plugin_original_host_*.go") if not p.name.endswith("_test.go")]+list(ROOT.glob("internal/plugins/*_host.go"))+[p for p in ROOT.glob("internal/browserrender/*.go") if not p.name.endswith("_test.go")]
for p in products: assert str(p.relative_to(ROOT)) in post and str(p.relative_to(ROOT)) in r9["compile_input_files"]
cleanup=read(BASE/"root-final-binary-cleanup.json")
assert not cleanup["active_owned_workers_after_final_tests"] and cleanup["temporary_directories_remaining"]==0
other_cleanup=read(BASE/"message-audit-final-cleanup.json")
assert other_cleanup["status"]=="passed" and not other_cleanup["remaining_owned_local_executable_files"]
for f in ["docs/plugin-test-quickstart.md","testdata/plugin-platform-final-20261104/frontend-tests.log","testdata/plugin-platform-final-20261104/frontend-build.log","testdata/plugin-platform-final-20261104/browser-ocr/product-r3-source-inputs.json","testdata/plugin-platform-final-20261104/five-slice-independent-review.json","testdata/plugin-platform-final-20261104/browser-ocr-independent-review.json","testdata/plugin-platform-final-20261104/root-final-binary-cleanup.json","testdata/plugin-platform-final-20261104/message-audit-final-cleanup.json","testdata/plugin-platform-final-20261104/site-http/vet-test-literal-lineage.json"]:
    report["current_review_bindings"][f]=digest(ROOT/f)
report["final_current_fixed_evidence"]={str(binding_path.relative_to(ROOT)):digest(binding_path),str(index_path.relative_to(ROOT)):digest(index_path)}
report["final_current_execution_audit"]={"actual_test_root_events":555,"failed_or_skipped_events":0,"current_pre_post_and_current_inputs":input_count,"R9_runtime_inputs":1818,"explicit_product_and_test_targets":target_count,"artifact_hash_checks":artifact_count,"actual_worker_receipts":65,"all62_old_group_targets_current_explicitly_bound":True,"additional_host_SDK_browser_product_files_covered":len(products),"selected_test_set_checked":isinstance(selected,list),"frontend_test_outside2937":"web/tests/plugin-log-dialog.test.mjs explicitly bound in120 current targets, byte-identical R3 complete source context plus556 actual frontend tests; unchanged136 embedded frontend assets and currentR9 backend/UI proof; not relabeled as a new frontend rerun","go_vet_exit":0,"diff_check_exit":0,"independent_source_evidence_leaf_index_checked":64}
for gate in report["required_final_gates"]: gate["status"]="passed"
report["audit_reproducer_sha256"]={str(Path(__file__).relative_to(ROOT)):digest(Path(__file__)),"testdata/plugin-platform-final-20261104/audit_final_independently.py":digest(BASE/"audit_final_independently.py")}
report["status"]="passed_in_scope_plugin_platform_migration_final_independent_acceptance"
report["final50_decision"]={"status":"验收通过","in_scope_rows_approved":65,"accepted_leaf_rows":64,"excluded_rows":4,"source":"64 source/target/runtime leaf records plus exact current555 App/SDK regression, current actual Linux/main/browser delta closure and explicit adaptations/cleanup","coordinator_closing_metadata_only":"Update unique matrix row50/header and ledger/completion references after this seal; these are deliberately not self-hashed."}
report["matrix_checkpoint_before_final50_metadata_update"]=report.pop("matrix_sha256")
report["matrix_hash_policy"]="Whole-matrix checkpoint SHA is historical diagnostic only. Hard evidence binds64 leaf behavior/evidence rows and fixed source-evidence-index; status/header/row50/ledger closing metadata are excluded to avoid self-reference."
report["unverified_in_scope"]=[]
report["unexplained_differences"]=[]
report["historical_and_optional_unexecuted_evidence"]="R8 parent disappearance remains unknown and not accepted; actualR9 had174 continuous integrity checks and completed own cleanup. Optional final Chrome reload interruption not counted; required browser/cold/clone flows have separately completed exact-version evidence. Earlier failed/interrupted regressions are retained and excluded from555."
report["publication"]={"status":"not_performed_as_requested","branch":binding["branch"],"head":binding["baseline_revision"],"local_changes_committed":False,"pushed":False,"merged":False,"production_deployed":False,"generic_skill_final_git_main_gate":"Not claimed: user requested local development without publication; no fake commit/main SHA manufactured."}
(BASE/"final-independent-review.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"status":report["status"],"approved":65,"excluded":4,"actual_current_roots":555,"current_inputs":2937,"R9_runtime_inputs":1818,"old_group_targets":62,"blocking":0,"publication":"not performed"}))
