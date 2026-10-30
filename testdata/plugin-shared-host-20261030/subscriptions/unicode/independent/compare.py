"""比较原版与实际宿主的持久事实；使用 Python 码点，不借用目标解析器。"""
import json,math,sys
from pathlib import Path
A=Path(sys.argv[1]);run=Path(sys.argv[2])
def canonical(v):
 if isinstance(v,bool):return ('bool',v)
 if isinstance(v,int):return ('int',v)
 if isinstance(v,float):
  if not math.isfinite(v):return ('nonfinite',repr(v))
  return ('float',v.hex())
 if isinstance(v,list):return ('list',tuple(map(canonical,v)))
 if isinstance(v,dict):
  if set(v)=={'$independent_nonfinite'}:return ('nonfinite',v['$independent_nonfinite'])
  return ('dict',tuple(sorted((k,canonical(x)) for k,x in v.items())))
 return (type(v).__name__,v)
reports=[]
for phase in ['warm','cold','repair']:
 source=json.loads((A/('source-'+phase+'.json')).read_text());target=json.loads((run/(phase+'.json')).read_text())
 for record in source['records']:
  actual=target['independent-unicode-'+str(record['id'])];fresh=record['fresh']
  assert actual['keyword']==fresh['keyword'],(phase,record['id'],'keyword',actual['keyword'],fresh['keyword'])
  fields=actual['source_fields'] or {}
  compared=[]
  for key in ['note','sites','filter_groups','episode_priority']:
   if key in fields:
    got=fields[key]['value'];want=fresh[key]
    assert canonical(got)==canonical(want),(phase,record['id'],key,ascii(got),ascii(want))
    compared.append(key)
  if not record.get('operation_error') and phase=='warm' and record['id']<16:assert len(compared)==4,(record['id'],compared)
  reports.append({'phase':phase,'case':record['name'],'json_columns_compared':compared,'keyword_equal':True})
result={'status':'passed','method':'Independent Python codepoint/type/float-sign semantic comparison of actual source DAO and target SQL raw overlay values. No Go JSON AST used to derive expectation.','observations':reports}
(run/'differential.json').write_text(json.dumps(result,indent=2)+'\n')
print('Independent semantic source/SQLite comparison passed:',len(reports),'observations')
