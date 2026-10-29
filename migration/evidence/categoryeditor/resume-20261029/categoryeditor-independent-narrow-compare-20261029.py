#!/usr/bin/env python3
"""Compare fresh independently executed original observations to target replay."""
from pathlib import Path
import hashlib,json,sys
E=Path(__file__).resolve().parent; phase=sys.argv[1] if len(sys.argv)>1 else 'final'
sp=E/'categoryeditor-independent-narrow-source-20261029.json'; tp=E/f'categoryeditor-independent-narrow-{phase}-target.json'; fs=E/'categoryeditor-independent-filesystem-source-20261029.json';seq=E/'categoryeditor-independent-root-read-sequence-20261029.json'
s=json.loads(sp.read_text());t=json.loads(tp.read_text());f=json.loads(fs.read_text());q=json.loads(seq.read_text());src={x['id']:x for x in s['cases']};checks=[]
def check(case,temp,field,a,b):checks.append({'id':case,'temperature':temp,'field':field,'source':a,'target':b,'equal':a==b})
def val(x):
 if 'entries' in x:return {str(val(k)):val(v) for k,v in x['entries']}
 a=x.get('value');return [val(i) for i in a] if isinstance(a,list) else a
def observed(x):return {'error':True} if 'exception' in x else {'value':val(x['return'])}
def target_observed(x,key):return {'error':True} if x.get(key+'_error') else {'value':x.get(key)}
def source_effects(trace):
 out=[]
 for x in trace:
  if x['event'] in ['systemmessage.put','put']:out.append({'method':'plugin.systemmessage.put','payload':{'message':x['args'][0],'title':x['kwargs']['title']}})
  if x['event']=='update_config':out.append({'method':'plugin.base.update_config','payload':{'config':x['config']}})
 return out
def target_effects(calls):return [x for x in calls if x['method']!='plugin.category.apply']
for row in t['cases']:
 for temp in ['warm','cold']:
  orig=src[row['id']][temp]['plugin_with_retained_archived_helper'];new=row[temp];look=orig['lookups_after_apply'];k=row['id']
  check(k,temp,'apply_error','exception' in orig['call'],bool(new.get('apply_error')))
  check(k,temp,'enabled',val(orig['enabled']),new['enabled'])
  check(k,temp,'file_base64',orig['user_file_after_base64'],new.get('file_base64'))
  check(k,temp,'effects',source_effects(orig['trace']),target_effects(new['calls']))
  check(k,temp,'getter_query_error',False,bool(new.get('query_error')))
  check(k,temp,'movie_categories',observed(look['movie_categories']),target_observed(new,'movie_categories'))
  check(k,temp,'movie_match',observed(look['movie_match']),target_observed(new,'movie_match'))
fsrows={x['id']:x for x in f['cases']}
for row in t['runtime_cases']:
 old=fsrows[row['id']];k=row['id']
 if k=='missing_file':check(k,None,'file_created',old['file_after_constructor'],row['file_created_by_read'])
 elif k=='external_change_without_init':check(k,None,'cached_match',old['match_without_reloading'],row['match_without_explicit_reload'])
 elif k=='write_only':
  check(k,None,'apply_error','apply_error' in old,row['apply_error']!='<nil>');check(k,None,'enabled',old['enabled'],row['enabled']);check(k,None,'cached_match',old['match_after_apply'],row['match_after_apply'])
  check(k,None,'effects',[x['method'] for x in source_effects(old['trace'])],[x for x in row['calls'] if x!='plugin.category.apply'])
oldsteps={x['id']:x for x in q['steps']}
for row in t.get('root_read_sequence',[]):
 old=oldsteps[row['id']];k=row['id'];check(k,None,'apply_error','exception' in old,bool(row.get('apply_error')));check(k,None,'enabled',old['enabled'],row['enabled']);check(k,None,'movie_match',old['movie_match'],row['movie_match']);check(k,None,'effects',source_effects(old['trace']),target_effects(row['calls']));check(k,None,'query_error',False,bool(row.get('query_error')))
output={'scope':'narrow controlled historical-source differential; callback database is a recorder; not installed full App / current NAS / browser acceptance','target_stable_during_probe':t['target_hashes_before_and_after_equal'],'input_files':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in [sp,tp,fs,seq,Path(__file__)]},'case_count':len(t['cases']),'runtime_case_count':len(t['runtime_cases']),'sequence_steps':len(t.get('root_read_sequence',[])),'check_count':len(checks),'mismatches':[x for x in checks if not x['equal']],'checks':checks}
output['pass']=not output['mismatches'] and output['target_stable_during_probe'];p=E/f'categoryeditor-independent-narrow-{phase}-comparison.json';p.write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');summary={k:v for k,v in output.items() if k not in ['checks','input_files','mismatches']};summary['mismatches']=[{k:(str(v)[:180]+'…' if len(str(v))>180 else v) for k,v in x.items()} for x in output['mismatches']];print(json.dumps(summary,ensure_ascii=False,indent=2));sys.exit(0 if output['pass'] else 1)
