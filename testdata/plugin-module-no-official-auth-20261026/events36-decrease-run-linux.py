import hashlib,json,os,pathlib,subprocess,sys,time
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
server=pathlib.Path(sys.argv[1])
env=os.environ.copy()
env.update({'TMPDIR':str(root/'events36-decrease-tmp'),'PLUGIN_PROCESS_SERVER_BINARY':str(server),'OWNED_INPUT_PROCESS_FIXTURE_BINARY':str(root/'services38-input-final-linux'),'OWNED_INPUT_FIXTURE_BINARY':str(root/'services38-input-final-linux'),'OWNED_EPISODES_PROCESS_FIXTURE_BINARY':str(root/'events36-episodes-linux'),'OWNED_EPISODES_REFRESH_FIXTURE_BINARY':str(root/'events36-episodes-linux'),'OWNED_COMPLETION_CHECK_FIXTURE_BINARY':str(root/'events36-completion-check-linux'),'OWNED_NOTICE_PROCESS_FIXTURE_BINARY':str(root/'events36-notice-linux'),'OWNED_NOTICE_FIXTURE_BINARY':str(root/'events36-notice-linux'),'OWNED_USER_MESSAGE_PROCESS_FIXTURE_BINARY':str(root/'events36-user-message-linux'),'PLUGIN_PROCESS_SUBSCRIPTION_DECREASE_FIXTURE':'true','PLUGIN_PROCESS_COMPLETION_TYPE':'tv','PLUGIN_PROCESS_SUBSCRIPTION_ORDINARY_FIXTURE':'true','PLUGIN_PROCESS_SUBSCRIPTION_NOTICE_FIXTURE':'true','PLUGIN_PROCESS_USER_MESSAGE_FIXTURE':'true','PLUGIN_PROCESS_SYNC_RECOGNIZE_ORACLE':str(root/'events36-recognize-source-oracle.json'),'PLUGIN_PROCESS_ASYNC_RECOGNIZE_ORACLE':str(root/'events36-async-recognize-source-oracle.json'),'PLUGIN_PROCESS_EVENTS36_EVIDENCE_DIR':str(root/'events36-decrease-evidence')})
(root/'events36-decrease-tmp').mkdir(exist_ok=True)
receipt={'kind':'actual Linux cmd/server and Go SDK gates; owned initial data/protocol preparation distinguished in tests','server':{'path':str(server),'sha256':hashlib.sha256(server.read_bytes()).hexdigest()},'test_binary':{'path':str(root/'events36-decrease-process-linux.test'),'sha256':hashlib.sha256((root/'events36-decrease-process-linux.test').read_bytes()).hexdigest()},'environment':{k:v for k,v in env.items() if k in ('TMPDIR',) or k.startswith('PLUGIN_PROCESS_') or k.startswith('OWNED_')},'runs':[]}
for name in ['TestPluginServicesProductionCLI','TestPluginSubscriptionCompletionProductionCLI']:
 cmd=[str(root/'events36-decrease-process-linux.test'),'-test.v','-test.run','^'+name+'$','-test.timeout','90s']
 log=root/('events36-decrease-'+('ordinary' if name=='TestPluginServicesProductionCLI' else 'completion')+'-linux.log')
 started=time.time()
 with log.open('w') as output: result=subprocess.run(cmd,env=env,cwd=root,stdout=output,stderr=subprocess.STDOUT)
 record={'test':name,'command':cmd,'exit_code':result.returncode,'elapsed_seconds':round(time.time()-started,3),'log':str(log),'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest()}
 receipt['runs'].append(record);(root/'events36-decrease-cli-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
 print(json.dumps(record),flush=True)
 print(log.read_text()[-8000:],flush=True)
 if result.returncode: raise SystemExit(result.returncode)
