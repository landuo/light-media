import json
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
mode = sys.argv[1]
out = root / 'work' / 'plugin-platform-final-20261104' / (sys.argv[2] if len(sys.argv) > 2 else 'combined-' + mode)
out.mkdir(parents=True, exist_ok=True)
(out / 'tmp').mkdir(exist_ok=True)
environment = os.environ.copy()
environment['TMPDIR'] = str(out / 'tmp')
race = ['-race'] if mode == 'race' else []
fixtures = [('LIGHT_MEDIA_CONCURRENT_FIXTURE', 'owned-concurrent-fixture', ''), ('OWNED_AUTH_FIXTURE_BINARY', 'owned-auth-fixture', ''), ('OWNED_AUTH_FRONTEND_FIXTURE_BINARY', 'owned-auth-fixture', '-X main.fixtureFrontend=true'), ('OWNED_MODULE_OVERRIDE_BINARY', 'owned-module-override', ''), ('OWNED_MODULE_OVERRIDE_V2_BINARY', 'owned-module-override', '-X main.version=2.0'), ('OWNED_MODULE_NO_DECLARATION_BINARY', 'owned-module-override', '-X main.declareModules=false'), ('OWNED_MODULE_FIXTURE_BINARY', 'owned-module-fixture', ''), ('OWNED_MODULE_FIXTURE_BINARY_V2', 'owned-module-fixture', '-X main.fixtureVersion=2.0'), ('OWNED_MODULE_FIXTURE_BINARY_V3', 'owned-module-fixture', ''), ('OWNED_STANDARD_FIXTURE_BINARY', 'owned-standard-fixture', ''), ('OWNED_STANDARD_FIXTURE_V2_BINARY', 'owned-standard-fixture', '-X main.fixtureVersion=2.0'), ('OWNED_STANDARD_DASHBOARD_META_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardMethods=multi'), ('OWNED_STANDARD_NO_DASHBOARD_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardMethods=none'), ('OWNED_BASE_FIXTURE_BINARY', 'owned-base-fixture', ''), ('ORIGINAL_API_FIXTURE_BINARY', 'original-api-fixture', ''), ('ORIGINAL_API_REGISTRATION_FIXTURE_BINARY', 'original-api-registration-fixture', ''), ('OWNED_APP_CONCURRENT_FIXTURE_BINARY', 'owned-app-concurrent-fixture', ''), ('OWNED_BRUSH_INVENTORY_FIXTURE_BINARY', 'owned-brush-inventory-fixture', ''), ('OWNED_BRUSH_INVENTORY_DENIED_FIXTURE_BINARY', 'owned-brush-inventory-fixture', '-X main.fixtureDenied=true'), ('OWNED_COMMAND_INGRESS_FIXTURE_BINARY', 'owned-command-ingress-fixture', ''), ('OWNED_DOWNLOADER_TASKS_FIXTURE_BINARY', 'owned-downloader-tasks-fixture', ''), ('OWNED_DOWNLOADER_TASKS_DENIED_FIXTURE_BINARY', 'owned-downloader-tasks-fixture', '-X main.fixtureDenied=true'), ('OWNED_EVENT_FIXTURE_BINARY', 'owned-event-fixture', ''), ('OWNED_EVENT_TARGET_FIXTURE_BINARY', 'owned-event-target-fixture', ''), ('OWNED_FRONTEND_FIXTURE_BINARY', 'owned-frontend-fixture', ''), ('OWNED_FRONTEND_FIXTURE_BINARY_V2', 'owned-frontend-fixture', '-X main.fixtureVersion=2.0'), ('OWNED_INSTANCE_LOG_FIXTURE_BINARY', 'owned-instance-log-fixture', ''), ('OWNED_INSTANCE_LOG_DENIED_FIXTURE_BINARY', 'owned-instance-log-fixture', '-X main.fixtureDenied=true'), ('OWNED_SUBMITTED_FIXTURE_BINARY', 'owned-submitted-fixture', ''), ('OWNED_ORDER_ALPHA', 'owned-handler-order-fixture', ''), ('OWNED_ORDER_BETA', 'owned-handler-order-fixture', '-X main.pluginID=OwnedOrderBeta -X main.order=-10 -X main.priority=20'), ('OWNED_ORDER_DEFAULT', 'owned-handler-order-fixture', '-X main.pluginID=OwnedOrderDefault -X main.order=0 -X main.defaultDeclaration=true'), ('OWNED_RESET_MUTATOR', 'owned-reset-generic-fixture', ''), ('OWNED_RESET_OBSERVER', 'owned-reset-generic-fixture', '-X main.pluginID=OwnedResetObserver -X main.priority=10 -X main.observer=true'), ('OWNED_COMMAND_MUTATOR', 'owned-command-register-fixture', ''), ('OWNED_COMMAND_OBSERVER', 'owned-command-register-fixture', '-X main.pluginID=OwnedCommandObserver -X main.priority=10 -X main.shorthand=true'), ('OWNED_CATALOG_SECOND_BINARY', 'owned-module-fixture', '-X main.fixturePluginID=OwnedCatalogSecond'), ('OWNED_CATALOG_CLONE_BINARY', 'owned-module-fixture', '-X main.fixturePluginID=OwnedModuleFixtureexact -X main.fixtureVersion=2.0'), ('OWNED_LICENSE_DENIED_BINARY', 'owned-standard-fixture', '-X main.fixtureVersion=2.0 -X main.fixtureAuthLevel=2'), ('OWNED_INPUT_FIXTURE_BINARY', 'owned-input-fixture', ''), ('OWNED_NOTICE_FIXTURE_BINARY', 'owned-notice-fixture', ''), ('OWNED_STANDARD_DASHBOARD_ZERO_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardParameters=[]'), ('OWNED_STANDARD_DASHBOARD_ONE_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardParameters=["user_agent"]'), ('OWNED_STANDARD_DASHBOARD_TWO_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardParameters=["key","user_agent"]'), ('OWNED_STANDARD_DASHBOARD_INVALID_FIXTURE_BINARY', 'owned-standard-fixture', '-X main.fixtureDashboardParameters=["key"]'), ('OWNED_LEGACY_ID_FIXTURE_BINARY', 'owned-module-fixture', '-X main.fixturePluginID=AutoSignIn'), ('OWNED_COMPLETION_CHECK_FIXTURE_BINARY', 'owned-command-register-fixture', '-X main.pluginID=OwnedCompletionCheck -X main.eventType=subscribe.completion.check'), ('OWNED_EPISODES_REFRESH_FIXTURE_BINARY', 'owned-command-register-fixture', '-X main.pluginID=OwnedEpisodesRefresh -X main.eventType=subscribe.episodes.refresh'), ('OWNED_USER_MESSAGE_FIXTURE_BINARY', 'owned-event-fixture', '-X main.extraEvent=user.message -X main.fixtureProcessAPI=true'), ('OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY', 'owned-downloader-control-fixture', ''), ('OWNED_TRANSFER_EVENTS_BINARY', 'owned-transfer-events-fixture', ''), ('OWNED_FRONTEND_PROCESS_FIXTURE_BINARY', 'owned-frontend-fixture', '-X main.fixtureProcessAPI=true'), ('OWNED_FRONTEND_PROCESS_FIXTURE_BINARY_V2', 'owned-frontend-fixture', '-X main.fixtureVersion=2.0 -X main.fixtureProcessAPI=true'), ('OWNED_HOST_BUSINESS_FIXTURE_BINARY', 'plugin-host-business-fixture', ''), ('OWNED_HOST_SERVICES_FIXTURE_BINARY', 'plugin-host-services-fixture', ''), ('OWNED_HOST_SERVICES_SERIAL_FIXTURE_BINARY', 'plugin-host-services-fixture', '-X main.fixtureSerial=true'), ('PLUGIN_HOST_DOWNLOADER_FIXTURE_BINARY', 'plugin-host-downloader-fixture', ''), ('OWNED_LOCAL_FILES_FIXTURE_BINARY', 'owned-local-files-fixture', ''), ('OWNED_HOST_NETWORK_FIXTURE_BINARY', 'plugin-host-network-fixture', ''), ('OWNED_HOST_MESSAGE_FIXTURE_BINARY', 'plugin-host-message-fixture', ''), ('OWNED_HOST_BROWSER_FIXTURE_BINARY', 'plugin-host-browser-fixture', ''), ('OWNED_CATALOG_EVENT_OVERRIDE_BINARY', 'owned-module-override', '-X main.fixtureCatalogEvents=true'), ('OWNED_TRANSFER_EVENT_OVERRIDE_BINARY', 'owned-module-override', '-X main.fixtureTransferEvents=true')]
with (out / 'build.log').open('w') as log:
 for key, command, flags in fixtures:
  binary = out / key.lower()
  arguments = ['go', 'build', *race, '-o', str(binary)]
  if flags:
   arguments += ['-ldflags', flags]
  arguments += ['./cmd/' + command]
  result = subprocess.run(arguments, cwd=root, env=environment, stdout=log, stderr=log)
  if result.returncode:
   print(json.dumps({'phase':'build', 'fixture':key, 'exit':result.returncode}), flush=True)
   sys.exit(result.returncode)
  environment[key] = str(binary)
(out / 'environment.json').write_text(json.dumps({key:environment[key] for key,_,_ in fixtures},indent=2))
print(json.dumps({'phase':'build', 'mode':mode, 'fixtures':len(fixtures), 'exit':0}), flush=True)
packages = ['./internal/app', './internal/plugins', './internal/plugins/artifacts', './internal/plugins/schedule', './web']
import re
roots=[]
for path in (root/'internal/app').glob('*test.go'):
 if path.name.startswith(('plugin_original','plugin_organization','plugin_standard','plugin_module_scope')):
  roots += re.findall(r'^func (Test\w+)\(', path.read_text(), re.M)
selection = '^(' + '|'.join(roots) + ')$'
# A test explicitly requiring richer source declarations gets its own actual
# executable. Never change an oracle to fit the generic fixture declaration.
transfer_roots = ['TestOriginalTransferAuxiliaryModuleNativePrecedence', 'TestOriginalTransferAuxiliaryNativePublishedColdRecovery', 'TestOriginalTransferBlurayNativeFailureAndRecovery', 'TestOriginalTransferBlurayUnitPreviewAndPublish', 'TestOriginalTransferCallbackFixedSourceFieldsAndHistory', 'TestOriginalTransferCallbackHistoryFailureCannotBroadcast', 'TestOriginalTransferCompletedActualImportSDK', 'TestOriginalTransferExecutionNamesOnlyNativeFallback', 'TestOriginalTransferInfoConstructorActualSDKFallback', 'TestOriginalTransferModuleActualReturnedTarget', 'TestOriginalTransferModuleActualSDKFixedSource', 'TestOriginalTransferModuleAssetCommitFailureColdRecovery', 'TestOriginalTransferModuleReturnedTargetColdRecovery', 'TestOriginalTransferNativeNamingPublishedColdRecovery', 'TestOriginalTransferNativeWithoutOverrideFlagsAndScrape', 'TestOriginalTransferPreviewActualSDKFixedSource', 'TestOriginalTransferRelatedManualPreviewActualSDK']
catalog_roots = ['TestOriginalCatalogOpaqueHTTPActualSDKConvertNameAndResolve']
groups = [('ordinary', [r for r in roots if r not in transfer_roots+catalog_roots], {}),
          ('transfer', transfer_roots, {'OWNED_MODULE_OVERRIDE_BINARY':environment['OWNED_TRANSFER_EVENT_OVERRIDE_BINARY']}),
          ('catalog', catalog_roots, {'OWNED_MODULE_OVERRIDE_BINARY':environment['OWNED_CATALOG_EVENT_OVERRIDE_BINARY']})]
(out / 'selected-roots.json').write_text(json.dumps(roots,indent=2))
result_codes = []
with (out / 'tests.jsonl').open('w') as log, (out / 'stderr.log').open('w') as stderr:
 for name, names, overrides in groups:
  arguments = ['go', 'test', *race, '-count=1', '-timeout=15m', '-json', '-run', '^('+'|'.join(names)+')$', './internal/app']
  result=subprocess.run(arguments,cwd=root,env=environment|overrides,stdout=log,stderr=stderr)
  result_codes.append({'group':name,'exit':result.returncode,'roots':len(names)})
(out / 'command-exits.json').write_text(json.dumps(result_codes,indent=2)+'\n')
exit_code = max(x['exit'] for x in result_codes)
summary = {'phase':'tests', 'mode':mode, 'exit':exit_code, 'packages':['./internal/app'], 'roots':{'pass':0,'fail':0,'skip':0}, 'failed':[], 'skipped':[]}
for line in (out / 'tests.jsonl').read_text().splitlines():
 try: row=json.loads(line)
 except ValueError: continue
 action, test = row.get('Action'), row.get('Test')
 if test and '/' not in test and action in summary['roots']:
  summary['roots'][action] += 1
  if action in ('fail', 'skip'): summary['failed' if action=='fail' else 'skipped'].append(row['Package']+':'+test)
(out / 'summary.json').write_text(json.dumps(summary, indent=2)+'\n')
print(json.dumps(summary), flush=True)
sys.exit(exit_code)
