"""Exact original core+endpoint bodies composed; only external boundaries synthetic."""
import ast,copy,hashlib,json,pathlib,tempfile,types,traceback
HERE=pathlib.Path(__file__).resolve().parent
previous=HERE.parent/'oracle-v2/run.py'
ns={'__file__':str(previous),'__name__':'combined_definitions'}
# Execute only the v2 definition/setup prefix, before its first case invocation.
prefix=previous.read_text().split("run('reset_real_stop_reload_form_remotes_apis'",1)[0]
exec(compile(prefix,str(previous),'exec'),ns)
records=[]

def setup(e,d,config,consume=False,behavior=None):
 m,g,c,kv,installed,P=ns['setup'](e,d)
 c['Source']=copy.deepcopy(config)
 more=ns['extract'](ns['ns']['CORE'],['get_plugin_config','save_plugin_config','delete_plugin_config','delete_plugin_data'],'PluginManager')
 ns['compile_nodes'](g,[ast.ClassDef(name='OriginalConfigurationMethods',bases=[],keywords=[],body=more,decorator_list=[])])
 for n in more:setattr(m,n.name,types.MethodType(getattr(g['OriginalConfigurationMethods'],n.name),m))
 m._config_key='plugin.%s'
 class Store:
  def get(self,key):return copy.deepcopy(installed if key=='installed' else c.get(key.removeprefix('plugin.')))
  def set(self,key,value):
   if key=='installed':installed[:]=value;e.append(['installed',copy.deepcopy(value)])
   else:c[key.removeprefix('plugin.')]=copy.deepcopy(value);e.append(['config.set',key,copy.deepcopy(value)])
   return True
  def delete(self,key):c.pop(key.removeprefix('plugin.'),None);e.append(['config.delete',key]);return True
 class Data:
  def del_data(self,pid):
   for k in list(kv):
    if k[0]==pid:del kv[k]
   e.append(['data.delete',pid])
 g.update(SystemConfigOper=Store,PluginDataOper=Data,Command=lambda:types.SimpleNamespace(init_commands=lambda pid:e.append(['commands.register',pid])),Scheduler=lambda:types.SimpleNamespace(update_plugin_job=lambda pid:e.append(['jobs.register',pid]),remove_plugin_job=lambda pid:e.append(['jobs.remove',pid])),register_plugin_api=lambda pid:e.append(['api.register',pid]),_add_clone_to_plugin_folder=lambda source,clone:e.append(['folder.clone',source,clone]))
 ns['compile_nodes'](g,ns['extract'](ns['ns']['API'],['register_plugin','reload_plugin','clone_plugin','set_plugin_config','reset_plugin','uninstall_plugin']))
 counts={}
 def init(self,conf):
  self.config=conf or {};e.append(['init',type(self).__name__,copy.deepcopy(conf)])
  name=type(self).__name__;counts[name]=counts.get(name,0)+1;ordinal=counts[name]
  if name=='Sourceowned':
   if behavior=='empty-first' and ordinal==1:
    Store().set('plugin.'+name,{});self.config={}
   if behavior=='raise-all' or (behavior=='raise-first' and ordinal==1) or (behavior=='raise-second' and ordinal==2):
    e.append(['synthetic.init.raise',name,ordinal]);raise RuntimeError('owned synthetic init failure '+str(ordinal))
  if consume and self.config.get('onlyonce'):
   value=copy.deepcopy(self.config);value['onlyonce']=False;Store().set('plugin.'+type(self).__name__,value);self.config=value
 P.init_plugin=init
 # Source is loaded before the measured endpoint operation, just as its precondition requires.
 m.start('Source');c['Source']=copy.deepcopy(config);e.clear()
 return m,g,c,kv,installed

def run(name,fn):
 e=[]
 try:
  with tempfile.TemporaryDirectory(prefix='owned-original-combined-') as tmp:result=fn(e,pathlib.Path(tmp))
  records.append({'case':name,'status':'pass','events':e,'result':result})
 except Exception as x:records.append({'case':name,'status':'fail','events':e,'error':str(x),'traceback':traceback.format_exc()})

def clone(e,d,config,consume=False):
 m,g,c,kv,installed=setup(e,d,config,consume)
 out=g['clone_plugin']('Source',{'suffix':'OWNED','name':'Owned','description':'Synthetic'})
 assert out['success'],out
 inits=[x for x in e if x[0]=='init' and x[1]=='Sourceowned']
 reloads=[x for x in e if x[:2]==['event.send','PluginReload']]
 expected=3 if config else 2
 assert len(inits)==expected,(len(inits),expected)
 assert len(reloads)==2,reloads
 if config:
  copied=next(x[2] for x in e if x[:2]==['config.set','plugin.Sourceowned'])
  expected_config=copy.deepcopy(config);expected_config.update(enable=False,enabled=False)
  assert copied==expected_config
 else:assert 'Sourceowned' not in c
 assert [x[0] for x in e if x[0].endswith('.register')]==['jobs.register','commands.register','api.register']
 assert e[-1]==['folder.clone','Source','Sourceowned']
 if consume:assert inits[0][2]['onlyonce'] is True and all(x[2]['onlyonce'] is False for x in inits[1:])
 return {'response':out,'initial_config':config,'clone_init_count':len(inits),'reload_event_count':len(reloads),'clone_config':c.get('Sourceowned'),'source_config':c['Source'],'source_kv':kv['Source','history'],'clone_kv_copied':('Sourceowned','history') in kv,'synthetic_init_consumes_once':consume}
run('endpoint_clone_nonempty_preserves_triggers',lambda e,d:clone(e,d,{'onlyonce':True,'clearcache':True,'plugin_ids':['OwnedTarget'],'count':0,'empty':''}))
run('endpoint_clone_empty_config',lambda e,d:clone(e,d,{}))
run('endpoint_clone_null_config',lambda e,d:clone(e,d,None))
run('endpoint_clone_rereads_config_changed_by_init',lambda e,d:clone(e,d,{'onlyonce':True,'enabled':True},True))

def clone_branch(e,d,behavior):
 m,g,c,kv,installed=setup(e,d,{'onlyonce':True,'enabled':True},behavior=behavior)
 out=g['clone_plugin']('Source',{'suffix':'owned'})
 inits=[x for x in e if x[:2]==['init','Sourceowned']]
 reloads=[x for x in e if x[:2]==['event.send','PluginReload']]
 registers=[x[0] for x in e if x[0].endswith('.register')]
 assert len(inits)==2,(behavior,inits)
 if behavior=='raise-second':
  assert not out['success'] and 'owned synthetic init failure 2' in out['message'],out
  assert len(reloads)==1 and registers==[] and not any(x[0]=='folder.clone' for x in e)
  assert 'Sourceowned' in m._running_plugins
 else:
  assert out['success'] and len(reloads)==2,out
  assert registers==['jobs.register','commands.register','api.register']
  assert ('Sourceowned' in m._running_plugins)==(behavior!='raise-all')
 if behavior=='empty-first':assert c['Sourceowned']=={} and inits[1][2]=={}
 assert 'Sourceowned' in installed and 'Sourceowned' in m._plugins
 assert (d/'app/plugins/sourceowned').is_dir()
 assert c['Source']=={'onlyonce':True,'enabled':True} and ('Sourceowned','history') not in kv
 return {'response':out,'synthetic_boundary_behavior':behavior,'clone_init_count':len(inits),'reload_event_count':len(reloads),'clone_config':c.get('Sourceowned'),'clone_class_present':'Sourceowned' in m._plugins,'clone_running':'Sourceowned' in m._running_plugins,'clone_installed':'Sourceowned' in installed,'clone_directory_present':True,'registration_order':registers}
for behavior in ['empty-first','raise-first','raise-all','raise-second']:
 run('endpoint_clone_'+behavior,lambda e,d,b=behavior:clone_branch(e,d,b))

def save(e,d):
 m,g,c,kv,installed=setup(e,d,{'enabled':False})
 out=g['set_plugin_config']('Source',{'enabled':True,'onlyonce':True})
 assert [x[0] for x in e]==['config.set','init','event.enable','cache.clear','jobs.register','commands.register','api.register'],e
 return {'response':out,'config':c['Source'],'loaded':list(m._running_plugins)}
run('endpoint_save_exact_order',save)

def reset(e,d):
 m,g,c,kv,installed=setup(e,d,{'enabled':True,'onlyonce':True});out=g['reset_plugin']('Source')
 kinds=[x[0] for x in e]
 assert kinds.index('event.send')<kinds.index('close')<kinds.index('config.delete')<kinds.index('data.delete')<kinds.index('init')<kinds.index('jobs.register')
 assert len([x for x in e if x[0]=='init'])==1 and 'Source' in m._running_plugins
 assert 'Source' not in c and ('Source','history') not in kv
 return {'response':out,'config_absent':True,'data_absent':True,'loaded':list(m._running_plugins)}
run('endpoint_reset_composed_stop_reload_register',reset)

def uninstall(e,d,isclone):
 m,g,c,kv,installed=setup(e,d,{'enabled':False,'value':7});pid='Source'
 if isclone:
  assert g['clone_plugin']('Source',{'suffix':'owned'})['success'];pid='Sourceowned';kv[pid,'history']='clone-only'
 e.clear();out=g['uninstall_plugin'](pid)
 kinds=[x[0] for x in e];assert kinds[:3]==['installed','api.remove','jobs.remove'];assert kinds.index('folder.remove')<kinds.index('close')
 assert pid not in m._running_plugins
 if isclone:assert pid not in c and (pid,'history') not in kv and not (d/'app/plugins'/pid.lower()).exists()
 else:assert c[pid]['value']==7 and kv[pid,'history']=='source-only' and (d/'app/plugins'/pid.lower()).exists()
 return {'response':out,'clone':isclone,'config_retained':pid in c,'kv_retained':(pid,'history') in kv,'code_directory_retained':(d/'app/plugins'/pid.lower()).exists()}
run('endpoint_uninstall_ordinary_order',lambda e,d:uninstall(e,d,False))
run('endpoint_uninstall_clone_order',lambda e,d:uninstall(e,d,True))
result={'ref':'8a83efeacd3e26b82be495e17d50146e31c96a60','pass':sum(x['status']=='pass' for x in records),'fail':sum(x['status']=='fail' for x in records),'cases':records,'scope':'Unchanged selected original core+endpoint AST bodies composed. Original start/stop/reload/config/clone plus endpoint clone/reload/register/save/reset/uninstall execute; discovery parses synthetic metadata and registration/store/events are recording boundaries. No real application/network/user files.'}
(HERE/'results.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
(HERE/'extracted-methods.json').write_text(json.dumps(ns['ns']['extracted'],ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'pass':result['pass'],'fail':result['fail']}))
for r in records:
 if r['status']=='fail':print(r['case'],r['traceback'])
raise SystemExit(bool(result['fail']))
