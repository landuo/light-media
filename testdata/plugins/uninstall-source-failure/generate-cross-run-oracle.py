"""Run the installed bounded init/Manager/SystemConfig AST twice using only owned state.
The real source methods come from the immutable R2 source snapshot. Commit and
cold cache replacement are synthetic; no original SQLAlchemy app is imported.
"""
import ast,copy,hashlib,json,pathlib,shutil,tempfile,threading
from types import SimpleNamespace as NS
from typing import Any,Union,Optional,List,Dict,Tuple
HERE=pathlib.Path(__file__).parent
source=(HERE/'generate-fault-oracle.py').read_text().split('cases=[case()]')[0]
# Reuse the existing fully bounded source-method fixture, preserving its exact
# first-run failure. Then start an independent init with source persisted config.
source=source.replace("def case(stage='',target='Alpha',clear=True):","def case(stage='',target='Alpha',clear=True,cold=False):")
needle="  except Exception as e:error=type(e).__name__\n  return "
insert="""  except Exception as e:error=type(e).__name__
  first={'error':error,'trace':copy.deepcopy(trace),'cache_installed':copy.deepcopy(cache['installed']),'durable_installed':copy.deepcopy(durable['installed']),'classes':list(manager._plugins)}
  stage='';trace.clear()
  if cold:
   cache.clear();cache.update(copy.deepcopy(durable))
   # Removed package directories remain absent. Source Manager.stop still
   # visits those IDs as a no-op; loaded class state is not the installed list.
  second_error=''
  try:pns['PluginUnInstall']().init_plugin(copy.deepcopy(durable['plugin.PluginUnInstall']))
  except Exception as e:second_error=type(e).__name__
  return {'cold':cold,'first':first,'second_error':second_error,'second_trace':trace,'cache_installed':cache['installed'],'durable_installed':durable['installed'],'classes':list(manager._plugins),'configs_retained':sorted(configs),'data_retained':sorted(data)}
  return """
if needle not in source:raise RuntimeError('fixture extraction changed')
source=source.replace(needle,insert)
ns=dict(globals());exec(compile(source,'owned-installed-cross-run-fixture','exec'),ns)
cases=[ns['case']('commit_installed',cold=cold) for cold in [False,True]]
out={'scope':'real installed bounded AST, synthetic commit/cache, owned directories; not a source full-app restart','snapshot_sha256':hashlib.sha256((HERE/'fault-source-without-metadata.json').read_bytes()).hexdigest(),'cases':cases}
p=HERE/'cross-run-oracle.json';p.write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'second_remove_jobs':[[x['target'] for x in c['second_trace'] if x['stage']=='remove_jobs_entry'] for c in cases]},indent=2))
