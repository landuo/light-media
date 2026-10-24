#!/usr/bin/env python3
"""Execute pinned original method ASTs; no translated menu-selection algorithm.

Only the original singleton constructor/application imports and unrelated methods
are omitted. The original method bodies, Pydantic event data declaration, and
DictUtils.filter_keys_to_subset are executed without rewriting their algorithms.
Owned fixtures replace only inputs, event delivery and final provider effects.
"""
from __future__ import annotations
import ast
import copy
import enum
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace
import threading
from typing import Dict, Optional
import pydantic
from pydantic import BaseModel, Field

HERE=Path(__file__).resolve().parent
REF=HERE.parent/'standard-command-ingress/reference'

verified=json.loads((HERE/'source-verification.json').read_text())
for row in verified:
 raw=(HERE/row['local']).read_bytes()
 assert hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()==row['git_blob_sha']
 assert hashlib.sha256(raw).hexdigest()==row['sha256']

ranges=[]
def class_node(path,name,methods=None):
 tree=ast.parse(path.read_text())
 node=copy.deepcopy(next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==name))
 if methods is not None:
  node.body=[n for n in node.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in methods]
  assert {n.name for n in node.body}==set(methods)
  node.bases=[];node.keywords=[];node.decorator_list=[]
  for n in node.body:
   ranges.append({'file':path.name,'symbol':name+'.'+n.name,'start':n.lineno,'end':n.end_lineno,'body_ast_sha256':hashlib.sha256(ast.dump(n,include_attributes=False).encode()).hexdigest()})
 return node

def execute(nodes,namespace,label):
 module=ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+nodes,type_ignores=[]))
 exec(compile(module,label,'exec'),namespace)

schema_ns={'__name__':'owned_command_oracle','BaseModel':BaseModel,'Field':Field,'Dict':Dict,'Optional':Optional}
execute([class_node(REF/'schemaEvent.py',n) for n in ['BaseEventData','ChainEventData','CommandRegisterEventData']],schema_ns,'original-command-event-schema')
EventData=schema_ns['CommandRegisterEventData']
EventData.model_rebuild(_types_namespace=schema_ns)
# The full original utility file has only imports, type variables, and classes.
utils={}
exec(compile((HERE/'reference/structures.py').read_text(),'original-structures.py','exec'),utils)
enum_cls=next(n for n in ast.parse((REF/'schema-types.py').read_text()).body if isinstance(n,ast.ClassDef) and n.name=='ChainEventType')
value=next(ast.literal_eval(n.value) for n in enum_cls.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='CommandRegister' for t in n.targets))
ChainEventType=enum.Enum('ChainEventType',{'CommandRegister':value})

class OwnedEvents:
 def __init__(self):self.mutate=None;self.active=False;self.input=None;self.final=None
 def send_event(self,kind,data):
  assert kind==ChainEventType.CommandRegister
  self.input=data.model_dump()
  event=SimpleNamespace(event_data=data)
  if self.active and self.mutate is not None:self.mutate(event)
  self.final=copy.deepcopy(event.event_data)
  return event if self.active else None

class OwnedProvider:
 def __init__(self):self.calls=[];self.fail=False
 def register_commands(self,*,commands):
  self.calls.append(copy.deepcopy(commands))
  if self.fail:raise RuntimeError('owned provider failure')

events=OwnedEvents();provider=OwnedProvider();logs=[]
namespace={'__name__':'owned_original_command','CommandRegisterEventData':EventData,'eventmanager':events,'CommandChain':lambda:provider,'ChainEventType':ChainEventType,'DictUtils':utils['DictUtils'],'logger':SimpleNamespace(debug=lambda *a,**kw:None,error=lambda *a,**kw:logs.append(str(a[0])))}
execute([class_node(REF/'command.py','Command',['__init_commands_background','__trigger_register_commands_event','__build_plugin_commands'])],namespace,'original-command-register-methods')
Command=namespace['Command']

# These are owned input declarations, not copied or rewritten menu logic.
presets={'/preset':{'description':'core','category':'management'},'/uncategorized':{'description':'no category'}}
plugin_rows=[{'cmd':'/plugin','desc':'plugin description','category':'owned','pid':'Owned','event':'owned.action','data':{'action':'never_an_sdk_method'}},{'cmd':'/hidden','desc':'hidden','pid':'Owned','show':False,'event':'owned.action','data':{}}]
other={'/other':{'description':'other input','category':'other','pid':'Other'}}

def new_command(previous=None):
 obj=Command();obj._rlock=threading.RLock();obj._preset_commands=copy.deepcopy(presets);obj._other_commands=copy.deepcopy(other);obj._plugin_commands={};obj._commands={'/stale':{'description':'stale'}};obj._registered_commands=copy.deepcopy(previous if previous is not None else {});obj.pluginmanager=SimpleNamespace(get_plugin_commands=lambda:copy.deepcopy(plugin_rows));obj.send_plugin_event=lambda:None
 return obj

def normalize(value):
 if isinstance(value,BaseModel):return normalize(value.model_dump())
 if isinstance(value,dict):return {k:normalize(v)for k,v in value.items() if k!='func'}
 if isinstance(value,(tuple,list)):return [normalize(v)for v in value]
 return value

results=[]
def run(name,mutate=None,pid=None,previous=None,active=True,provider_fail=False):
 events.active=active;events.mutate=mutate;provider.calls=[];provider.fail=provider_fail;logs.clear()
 obj=new_command(previous)
 obj._Command__init_commands_background(pid)
 result={'case':name,'pid':pid,'previous':previous,'initial':events.input,'final_event_data':normalize(events.final),'provider_calls':copy.deepcopy(provider.calls),'registered':copy.deepcopy(obj._registered_commands),'execution':normalize(obj._commands),'errors':logs.copy()}
 assert '/stale' not in obj._commands and '/plugin' in obj._commands and '/hidden' in obj._commands
 result=normalize(result);results.append(result);return result

def cancel(e):e.event_data.cancel=True;e.event_data.source='Owned'
def menu(e,commands,source=None):
 e.event_data.commands=copy.deepcopy(commands)
 if source is not None:e.event_data.source=source

base=run('no_handlers',active=False)
initial=base['registered'];assert len(base['provider_calls'])==1
r=run('unchanged',previous=initial);assert not r['provider_calls']
r=run('cancel_over_force',cancel,pid='Owned',previous={'/old':{'description':'retained'}});assert not r['provider_calls'] and '/old' in r['registered']
r=run('changed_subset',lambda e:menu(e,{'/plugin':{'description':'menu-only','opaque':900719925474099312345},'/unknown':{'description':'discard'}}));assert r['registered']=={'/plugin':{'description':'menu-only','opaque':900719925474099312345}} and r['execution']['/plugin']['description']=='plugin description'
r=run('hidden_known_name_can_return',lambda e:menu(e,{'/hidden':{'description':'explicitly selected'}}));assert '/hidden' in r['registered']
r=run('force_unchanged',lambda e:setattr(e.event_data,'source','Owned'),pid='Owned',previous=initial);assert len(r['provider_calls'])==1
r=run('source_mismatch_unchanged',lambda e:setattr(e.event_data,'source','Other'),pid='Owned',previous=initial);assert not r['provider_calls']
r=run('startup_unscoped_unchanged',lambda e:setattr(e.event_data,'source','Owned'),previous=initial);assert not r['provider_calls']
r=run('empty_skip',lambda e:menu(e,{}),previous=initial);assert not r['provider_calls'] and r['registered']==initial
r=run('empty_force',lambda e:menu(e,{},'Owned'),pid='Owned',previous=initial);assert r['provider_calls']==[{}] and r['registered']=={}
r=run('unknown_only_skip',lambda e:menu(e,{'/unknown':{}}),previous=initial);assert not r['provider_calls'] and r['registered']==initial
r=run('unknown_only_force',lambda e:menu(e,{'/unknown':{}},'Owned'),pid='Owned',previous=initial);assert r['provider_calls']==[{}]
r=run('provider_failure_keeps_selected_snapshot',lambda e:menu(e,{'/plugin':{'description':'new'}}),provider_fail=True);assert r['registered']=={'/plugin':{'description':'new'}} and len(r['errors'])==1
# Deliberate dynamic assignments are legal because original Pydantic model does
# not enable validate_assignment. The original utility decides their semantics.
for name,value in [('list',['not','a','dict']),('string','not-a-dict')]:
 r=run('truthy_'+name+'_force',lambda e,v=value:menu(e,v,'Owned'),pid='Owned',previous=initial);assert r['provider_calls']==[{}] and not r['errors']
 r=run('truthy_'+name+'_skip',lambda e,v=value:menu(e,v),previous=initial);assert not r['provider_calls'] and not r['errors'] and r['registered']==initial
for name,value in [('integer',7),('boolean',True)]:
 for forced in [False,True]:
  r=run('truthy_'+name+('_force' if forced else '_skip')+'_errors_before_filter',lambda e,v=value,f=forced:menu(e,v,'Owned' if f else None),pid='Owned',previous=initial)
  assert not r['provider_calls'] and len(r['errors'])==1 and r['registered']==initial
for name,value in [('zero',0),('false',False),('null',None),('empty_list',[]),('empty_string','')]:
 for forced in [False,True]:
  r=run('falsey_'+name+('_force' if forced else '_skip'),lambda e,v=value,f=forced:menu(e,v,'Owned' if f else None),pid='Owned',previous=initial)
  assert not r['errors'] and r['provider_calls']==([{}] if forced else []) and r['registered']==({} if forced else initial)
r=run('explicit_empty_pid_matches_empty_source',lambda e:setattr(e.event_data,'source',''),pid='',previous=initial);assert len(r['provider_calls'])==1

def replace_falsey(e):e.event_data=None
r=run('falsey_event_data_falls_back',replace_falsey,previous={});assert r['registered']==initial
# The original model copies the incoming dictionaries. Capture this explicitly;
# do not infer Python alias semantics from the host JSON transport.
def mutate_then_falsey(e):
 e.event_data.commands['/plugin']['description']='edited before replacing data'
 e.event_data=None
r=run('mutate_then_falsey_event_data',mutate_then_falsey,previous={})
assert len(r['provider_calls'])==1

output={'scope':'Only original Command registration method bodies and original schema/DictUtils; owned eventmanager/provider boundaries. No Go comparison execution or notification channels.','python_pydantic':pydantic.__version__,'source_verification':'source-verification.json','methods':ranges,'passed_cases':len(results),'cases':results}
(HERE/'oracle-results.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'passed_cases':len(results),'methods':len(ranges),'list_force_provider_calls':next(r['provider_calls'] for r in results if r['case']=='truthy_list_force'),'mutate_then_falsey_description':r['registered']['/plugin']['description']},ensure_ascii=False))
