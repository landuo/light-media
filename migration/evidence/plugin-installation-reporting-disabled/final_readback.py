from pathlib import Path
import datetime, hashlib, json, re, subprocess
ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261102T1545')
WORK=ROOT/'target-drafts/s08-git-delivery-worktree01'
EVIDENCE=ROOT/'evidence/parent23-no-report-20261104'
OUT=ROOT/'audits/parent23-current-closure-independent-20261104'
def rec(p):
 p=Path(p);b=p.read_bytes();return {'path':str(p),'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()}
def require(x,msg):
 if not x:raise RuntimeError(msg)
expected_roots={
'TestOriginalPluginInstallationReportsHaveNoSideEffects',
'TestOriginalPluginReportedRepositoryLegacySanitization',
'TestOriginalPluginServerActualHTTPProtocolAndFailureSemantics',
'TestOriginalPluginServerReportingHasNoSideEffectsAndPreservesLegacyReceipt',
'TestOriginalPluginServerSettingsAndRedirectBoundary',
'TestOriginalPluginCenterRestartNeverReportsOrChangesLegacyReceipt',
'TestOriginalPluginCenterActualInstallNeverReportsAndPreservesMaterialQueries'}
r={'status':'ACCEPT_CURRENT_PARENT23_NO_AUTOMATIC_INSTALLATION_REPORTING_PREPUBLICATION','at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'reviewer_executed_tests':False,'reviewer_modified_product_or_ledger':False,'reviewer_network_requests':False,'whole_current_parent_scope_accepted':True,'remote_publication_verified':False,'supersedes':'Earlier query-retention route is historical only. User now requests no automatic installation statistics; source-equivalent report emission/receipt behavior is intentionally superseded.','results':{},'files':[]}
for phase in ['normal','race','vet','frontend-tests','frontend-build','worker-normal-v1','worker-normal-v2','worker-race-v1','worker-race-v2']:
 result=json.loads((EVIDENCE/f'{phase}-result.json').read_text());command=json.loads((EVIDENCE/f'{phase}-command.json').read_text());require(result['exit']==0,phase+' exit')
 checks=[]
 for name,wanted in result.get('files', {}).items():
  got=rec(EVIDENCE/name);require(got['bytes']==wanted['bytes'] and got['sha256']==wanted['sha256'],phase+' captured log binding');checks.append(got)
 item={'exit':result['exit'],'started':result['started'],'ended':result['ended'],'command':rec(EVIDENCE/f'{phase}-command.json'),'result':rec(EVIDENCE/f'{phase}-result.json'),'logs':checks}
 if phase in ['normal','race']:
  stdout=(EVIDENCE/f'{phase}.stdout').read_text();stderr=(EVIDENCE/f'{phase}.stderr').read_text()
  roots=set(re.findall(r'^--- PASS: (\S+) ',stdout,re.M));runs=set(re.findall(r'^=== RUN   (\S+)\s*$',stdout,re.M))
  require(roots==expected_roots,phase+' root set');require(not re.search(r'^--- (FAIL|SKIP):',stdout,re.M),phase+' failure or skip');require('WARNING: DATA RACE' not in stdout+stderr,phase+' race warning');require(not stderr,phase+' nonempty stderr');require(result['project_input_count']==3234 and result['project_input_drift']==[],phase+' input drift')
  item.update({'root_passes':sorted(roots),'root_count':len(roots),'root_fail_or_skip':0,'subcase_count':len(runs-expected_roots),'project_input_count':3234,'project_input_drift':[]})
  if phase=='race':
   require('-race' in command['argv'],'host race flag')
   for key,filename in [('OWNED_STANDARD_FIXTURE_BINARY','worker-race-v1'),('OWNED_STANDARD_FIXTURE_V2_BINARY','worker-race-v2')]:require(command['environment'][key].endswith('/'+filename),'race worker binding')
 if phase.startswith('worker-'):
  if 'race' in phase:require('-race' in command['argv'],'worker race flag')
  output=Path(command['argv'][command['argv'].index('-o')+1]);item['current_worker_bytes']=rec(output);wanted=result['binary'];require(item['current_worker_bytes']['bytes']==wanted['bytes'] and item['current_worker_bytes']['sha256']==wanted['sha256'],'worker binary after build binding');item['binary_binding_scope']='Current bytes match recorded successful build output before both selected executions.'
 if phase=='frontend-tests':
  stdout=(EVIDENCE/f'{phase}.stdout').read_text();require('tests 25' in stdout and 'pass 25' in stdout and 'fail 0' in stdout and 'skipped 0' in stdout,'frontend totals')
 if phase=='frontend-build':item['nonfatal_warnings']='Recorded npm unknown http-proxy config, future Vite config loader JSON import, chunk size, and npm update notices. Not warning-free.'
 r['results'][phase]=item
before=json.loads((EVIDENCE/'project-inputs-before.json').read_text());after=json.loads((EVIDENCE/'project-inputs-after.json').read_text());require(before==after and len(before)==3234,'before/after maps')
drift=[]
for name,sha in before.items():
 p=WORK/name
 if not p.exists() or rec(p)['sha256']!=sha:drift.append(name)
require(not drift,'current project drift')
r['current_project_binding']={'files':3234,'before_after_equal':True,'current_drift':drift,'scope':'Recorded project Go/mod/sum and frontend distribution inputs, not all native/compiler/OS inputs.','before':rec(EVIDENCE/'project-inputs-before.json'),'after':rec(EVIDENCE/'project-inputs-after.json')}
source_paths=['internal/app/plugin_original_server.go','internal/app/plugin_original_server_body.go','internal/app/plugin_original_server_validation.go','internal/app/plugin_original_install.go','internal/app/plugin_original_sync.go','internal/app/plugin_original_reported_repository_test.go','internal/app/plugin_original_server_test.go','internal/app/plugin_original_server_lifecycle_test.go','internal/app/plugin_module_process_center_test.go','web/src/components/PluginSystemSettings.vue','web/tests/plugin-market-settings.test.mjs','web/dist/index.html','docs/plugin-installation-reporting-disabled-20261104.md','docs/plugin-usage-rating-20261030.md']
r['files']=[rec(WORK/p) for p in source_paths]
head=subprocess.check_output(['git','-C',str(WORK),'show','HEAD:internal/app/plugin_original_server.go']);current=(WORK/'internal/app/plugin_original_server.go').read_bytes();offset=head.index(b'func (a *App) reportOriginalPluginInstallation(')
require(head[:offset]==current[:offset],'unchanged shared query/rating/body prefix')
require(b'func (a *App) reportOriginalPluginInstallation(context.Context, string, string) {}' in current and b'func (a *App) reportOriginalPluginInstallations(context.Context) {}' in current,'no-op exact bodies')
r['code_findings']={'request_statistics_rating_and_sanitizer_prefix_unchanged':True,'both_reporting_functions_empty':True,'no_installation_post_constructors_remain_in_product':True,'full_repository_acquisition_paths_unchanged':True,'persistent_true_cannot_reenable_reporting':True,'legacy_receipts_not_deleted_or_written':True,'no_claim_all_movie_pilot_org_traffic_disabled':True}
r['historical_inheritance']={'TLS_body':'Existing parent24 actual owned app/pure evidence retained for unchanged statistics/rating shared paths; current body helper unchanged. Earlier upload-positive behavior intentionally superseded.','query':'Old original raw absent; 11-02 new source observation retained. Both prior query-only test and accepted keep-query decision superseded by no-upload request.','ui_and_cli':'Historical ordinary protocol/UI/whole CLI credit retained where unchanged. This run does not claim a new ordinary cmd/server full CLI or browser rerun.'}
r['limits']=['Current exact seven roots in real full App package, actual v1/v2 workers and seven subprocess restarts, not the whole Go test suite.','Changed module-process center expectation helper compiled with package but its complete ordinary production CLI parent root was not re-executed.','Component tests and frontend build plus narrow asset readback, not a browser run.','Owned HTTP fixture only; no real statistics-center install report sent.','Remote commit/push and final ledger progress snapshot remain parent-owned publication steps.']
(OUT/'NO-REPORTING-FINAL.json').write_text(json.dumps(r,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':r['status'],'normal_roots':r['results']['normal']['root_count'],'race_roots':r['results']['race']['root_count'],'current_inputs':len(before),'current_drift':drift,'final':rec(OUT/'NO-REPORTING-FINAL.json')},ensure_ascii=False))
