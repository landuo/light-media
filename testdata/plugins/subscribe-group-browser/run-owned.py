from pathlib import Path
import json, subprocess, signal, hashlib
base = Path(__file__).resolve().parent
root = base/'run-ui-2'
root.mkdir(exist_ok=True)
for name in ('home','tmp'): (root/name).mkdir(exist_ok=True)
for name in ('ready.json','completed.json','stop','native-fixture-exit.json'):
 if (root/name).exists(): raise RuntimeError('existing evidence requires a fresh owned run directory: '+name)
worker = base/'server'
expected = json.loads((base/'build-summary.json').read_text())['worker_sha256']
if hashlib.sha256(worker.read_bytes()).hexdigest()!=expected: raise RuntimeError('worker build digest changed')
fixture_expected=json.loads((base/'build-summary.json').read_text())['fixture_sha256']
if hashlib.sha256((base/'fixture.test').read_bytes()).hexdigest()!=fixture_expected: raise RuntimeError('fixture build digest changed')
env={'PATH':'/usr/bin:/bin','HOME':str(root/'home'),'TMPDIR':str(root/'tmp'),'LANG':'C.UTF-8','TZ':'UTC','GOMAXPROCS':'2','SUBSCRIBEGROUP_BROWSER_DIR':str(root),'SUBSCRIBEGROUP_BROWSER_WORKER':str(worker),'SUBSCRIBEGROUP_BROWSER_WORKER_SHA256':expected}
with (root/'native-fixture.stdout').open('wb') as out,(root/'native-fixture.stderr').open('wb') as err:
 p=subprocess.Popen([str(base/'fixture.test'),'-test.run=^TestSubscribeGroupBrowserOwnedFormFixture$','-test.timeout=21m','-test.v'],cwd=str(base.parent.parent/'source/internal/app'),env=env,stdout=out,stderr=err,start_new_session=True)
 def stop(*_): (root/'stop').touch()
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
 try: code=p.wait(timeout=1270)
 except subprocess.TimeoutExpired:
  p.terminate()
  try: code=p.wait(timeout=5)
  except subprocess.TimeoutExpired: p.kill();code=p.wait()
 (root/'native-fixture-exit.json').write_text(json.dumps({'exit':code,'owned_child_reaped':True})+'\n')
 print('owned SubscribeGroup browser fixture finished:',code,flush=True)
