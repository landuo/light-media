"""Replay fixed scrape batching methods with controlled task DTOs and event collector.
No light-media implementation participates in expected results. This is source
replay, not a running MoviePilot installation; file publication is tested separately.
"""
import ast, copy, json, subprocess, threading
from pathlib import Path
from types import SimpleNamespace as NS
FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',FIXED+':app/chain/transfer.py'],text=True)
tree=ast.parse(raw)
names=['__register_scrape_batch_task','__close_scrape_batch','__record_scrape_target','__finish_scrape_batch_task','__flush_scrape_batch_if_ready']
methods=[copy.deepcopy(n) for n in ast.walk(tree) if isinstance(n,ast.FunctionDef) and n.name in names]
for method in methods:
 method.returns=None
 for arg in method.args.args:arg.annotation=None
cls=ast.ClassDef(name='TransferChain',bases=[],keywords=[],body=methods,decorator_list=[])
ns={'job_lock':threading.Lock(),'EventType':NS(MetadataScrape='metadata.scrape')}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'<fixed:transfer.py scrape batch>','exec'),ns)
def plain(x):
 if isinstance(x,NS):return {k:plain(v) for k,v in vars(x).items()}
 if isinstance(x,dict):return {k:plain(v) for k,v in x.items()}
 if isinstance(x,list):return [plain(v) for v in x]
 return x
cases=[]
for name,paths,roots,lists,enabled,success,close_first in [
 ('same-root', ['a.mkv','b.mkv'],['/owned/show/','/owned/show/'],[['/owned/show/a.mkv'],['/owned/show/b.mkv']], [True,True],[True,True],True),
 ('distinct-roots',['a.mkv','b.mkv'],['/owned/a/','/owned/b/'],[['/owned/a/a.mkv'],['/owned/b/b.mkv']],[True,True],[True,True],True),
 ('duplicates-and-empty',['a.mkv','b.mkv'],['/owned/show/','/owned/show/'],[['/owned/show/a.mkv','', '/owned/show/a.mkv'],['/owned/show/a.mkv','/owned/show/b.mkv']],[True,True],[True,True],True),
 ('failed-last',['a.mkv','b.mkv'],['/owned/show/','/owned/show/'],[['/owned/show/a.mkv'],['/owned/show/b.mkv']],[True,True],[True,False],True),
 ('disabled-and-subtitle',['a.mkv','b.mkv','a.srt'],['/owned/show/']*3,[['/owned/show/a.mkv'],['/owned/show/b.mkv'],['/owned/show/a.srt']],[True,False,True],[True]*3,True),
 ('close-after-finish',['a.mkv','b.mkv'],['/owned/show/']*2,[['/owned/show/a.mkv'],['/owned/show/b.mkv']],[True]*2,[True]*2,False),
 ('all-failed',['a.mkv','b.mkv'],['/owned/show/']*2,[[],[]],[True]*2,[False]*2,True),
]:
 events=[]; chain=ns['TransferChain']();chain._scrape_batches={}
 chain._TransferChain__is_media_file=lambda f:f.path.endswith('.mkv')
 chain.eventmanager=NS(send_event=lambda kind,data:events.append(plain(data)))
 tasks=[NS(transfer_batch_id='batch',fileitem=NS(path=p),meta={'title':'first' if i==0 else 'later'},mediainfo={'tmdb_id':123}) for i,p in enumerate(paths)]
 call=lambda m,*a:getattr(chain,'_TransferChain'+m)(*a)
 for t in tasks:call('__register_scrape_batch_task',t)
 if close_first:call('__close_scrape_batch','batch')
 counts=[]
 for i,t in enumerate(tasks):
  if success[i]:call('__record_scrape_target',t,NS(need_scrape=enabled[i],target_diritem=NS(storage='local',path=roots[i],type='dir'),file_list_new=lists[i]))
  call('__finish_scrape_batch_task',t);counts.append(len(events))
 if not close_first:call('__close_scrape_batch','batch')
 call('__close_scrape_batch','batch') # no replay after already flushed
 cases.append(dict(name=name,paths=paths,roots=roots,lists=lists,enabled=enabled,success=success,close_first=close_first,counts=counts,events=events))
out=dict(fixed_backend=FIXED,source=['app/chain/transfer.py:1502-1627'],boundary=__doc__,cases=cases)
Path(__file__).with_name('scrape-batch-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(len(cases),'fixed scrape batch cases')
