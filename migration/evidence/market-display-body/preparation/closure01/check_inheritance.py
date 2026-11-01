"""Read-only current/historical byte attribution; writes only this draft directory."""
import datetime,hashlib,json,re,subprocess
from pathlib import Path
ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');A=ROOT/'release-light-media';D=Path(__file__).resolve().parent
BASE='5f829f8d6dba7572e48b8bf3d261c99e8f2447da';MARKET='122fafc7b4ee93589250cb82be849fead48e79d1';UI='b4f3251ee4f5359659a66935a427502da20f59e1'
def digest(raw):return hashlib.sha256(raw).hexdigest()
def row(p):
 b=p.read_bytes();return {'path':str(p),'bytes':len(b),'sha256':digest(b)}
def git(*args):return subprocess.check_output(['git','-C',str(A),*args])
assert git('rev-parse','HEAD').decode().strip()==BASE
old=json.loads((A/'migration/evidence/market-pipeline/candidate.json').read_text())
checks=[]
for rel,expected in old['files'].items():
 current=row(A/rel);oldblob=git('show',MARKET+':'+rel)
 checks.append({'relative':rel,'historical_candidate_sha256':expected,'old_committed_blob_sha256':digest(oldblob),
                'current':current,'same_as_historical':current['sha256']==expected,'old_blob_matches_candidate':digest(oldblob)==expected})
assert all(x['old_blob_matches_candidate'] for x in checks)
changed=[x['relative'] for x in checks if not x['same_as_historical']]
assert changed==['internal/app/http.go','internal/app/plugin_original_market_cache.go','web/dist/index.html']
(D/'three-known-deltas.patch').write_bytes(git('diff',MARKET,BASE,'--',*changed))
(D/'accepted-statistics-ui-delta.patch').write_bytes(git('diff',MARKET,BASE,'--','web/src/components/Plugins.vue'))
assert not git('diff','--name-only',UI,BASE,'--','web/src','web/dist').strip()
ui=json.loads((A/'migration/evidence/service-usage-runtime/candidate.json').read_text())
frontend=json.loads((A/'migration/evidence/service-usage-runtime/frontend-current.json').read_text())
closure=json.loads((A/'migration/evidence/service-usage-runtime/asset-closure.json').read_text())
reachable=closure['reachable']
if isinstance(reachable,dict):reachable=list(reachable)
assert all(isinstance(x,str) for x in reachable)
assets=[]
for rel in reachable:
 current=row(A/rel);expected=frontend['dist'][rel]
 assert current['sha256']==expected,rel
 assets.append({'relative':rel,'sha256':expected,'bytes':current['bytes']})
assert len(assets)==134
html=(A/'web/dist/index.html').read_text();entry=re.search(r'<script[^>]+src="\.\/([^\"]+)"',html).group(1)
index=(A/'web/dist'/entry).read_text();assert 'import(`./Plugins-CWrbcVZf.js`)' in index
plugins=(A/'web/dist/assets/Plugins-CWrbcVZf.js').read_text()
history_names=sorted(set(re.findall(r'PluginVersionHistoryDialog-[A-Za-z0-9_-]+\.js',plugins)))
assert len(history_names)==1
selected=['web/src/components/Plugins.vue','web/src/components/PluginVersionHistoryDialog.vue','web/src/api.ts',
          'internal/app/plugin_original_server_compat.go','internal/app/plugin_original_server_validation.go','internal/app/plugin_services_wire.go',
          'internal/app/plugin_original_requestutils_tls.go','internal/app/plugin_original_market_package_cache.go',
          'internal/app/plugin_original_market_refresh.go','internal/app/plugin_original_release_cache.go','internal/app/plugin_original_sync.go',
          'internal/plugins/artifacts/acquire.go']
extras=[]
for rel in selected:
 r=row(A/rel);assert r['sha256']==digest(git('show',BASE+':'+rel)),rel;extras.append(r)
ledger=json.loads((A/'migration/ledger.json').read_text());parents=[]
for f in ledger['features']:
 if f.get('id') in ('08','09','10','12'):
  p=f['parent_acceptance_current'];parents.append({'id':f['id'],'description':f['description'],'scope':p['scope'],'remaining_snapshot':p['remaining'],
    'historical_operations':f['historical_contract_operations'],'original_historical_credit':f['historically_executed_evidence_verbatim'],
    'status_snapshot':p['status'],'not_authoritative_ledger':True})
assert len(parents)==4
budget=json.loads((ROOT/'audits/resource-budget-current01/readback.json').read_text())
market=[x for x in budget['target_files'] if x['relative'].startswith('internal/app/plugin_original_') or x['relative'] in ('internal/plugins/artifacts/acquire.go','internal/plugins/artifacts/github_repository.go','internal/plugins/artifacts/python_metadata.go')]
assert all(row(Path(x['path']))['sha256']==x['sha256'] for x in market)
source=budget['source_files'];assert all(row(Path(x['path']))['sha256']==x['sha256'] for x in source)
references=[A/'migration/evidence/market-pipeline/independent-review.json',A/'migration/evidence/market-pipeline/candidate.json',
 A/'migration/evidence/market-pipeline/shared-impact.json',A/'migration/evidence/service-usage-runtime/candidate.json',
 A/'migration/evidence/service-usage-runtime/frontend-current.json',A/'migration/evidence/service-usage-runtime/asset-closure.json',
 A/'migration/evidence/service-usage-runtime/browser-independent.json',A/'migration/evidence/service-usage-runtime/browser-current.json',
 A/'migration/evidence/service-usage-runtime/statistics-source-frontend.json',A/'docs/validation/plugin-module-no-official-auth-20261026.json',
 ROOT/'audits/resource-budget-current01/final-seal.json',ROOT/'audits/platform-requestutils-a-publication01/push-readback-receipt.json']
result={'status':'STATIC_INHERITANCE_PROOF_CLI_PENDING_NOT_PARENT_ACCEPTANCE','at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
 'base':BASE,'historical_market_commit':MARKET,'accepted_later_UI_commit':UI,'old_market_bindings':checks,
 'same':len(checks)-len(changed),'changed':changed,'current_embedded_UI_assets_exact_previous_accepted':assets,
 'actual_loaded_UI_entry':entry,'actual_loaded_Plugins':'assets/Plugins-CWrbcVZf.js','actual_loaded_history_component':history_names,
 'whole_web_src_and_dist_diff_since_accepted_UI':[],'current_extra_bindings':extras,'prior_budget_market_leaves_unchanged':len(market),
 'prior_fixed_source_bindings_unchanged':len(source),'parent_contracts':parents,'references':[row(p) for p in references],
 'known_original_raw_unavailable':'independent-current02/comparison.json is not recovered; only prior committed review retained. New parser6 is distinct actual execution.',
 'not_claimed':['new browser/worker execution','full original HTTP/cache collector','four completed parents before actual CLI/independent seal','remote delivery']}
(D/'inheritance-readback.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'same':result['same'],'changed':changed,'reachable_UI':len(assets),'parents':len(parents),'output':row(D/'inheritance-readback.json')}))
