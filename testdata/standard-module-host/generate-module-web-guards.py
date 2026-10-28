"""Read-only fixed Web message guards. No target IM/AI implementation is added."""
import ast,copy,dataclasses,enum,hashlib,json,subprocess,typing
from pathlib import Path
from types import SimpleNamespace as NS
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';root='/Users/accidia/projects/MoviePilot';paths=['app/schemas/types.py','app/schemas/message.py','app/chain/__init__.py','app/chain/message.py','app/api/endpoints/message.py'];sources={p:subprocess.check_output(['git','show',fixed+':'+p],cwd=root)for p in paths}
ns=vars(typing).copy();ns.update(Enum=enum.Enum,dataclass=dataclasses.dataclass,logger=NS(debug=lambda *a:None))
nodes=[]
for p,names in [(paths[0],{'MessageChannel'}),(paths[1],{'ChannelCapability','ChannelCapabilities','ChannelCapabilityManager'})]:nodes +=[copy.deepcopy(n)for n in ast.parse(sources[p]).body if isinstance(n,ast.ClassDef)and n.name in names]
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'fixed-message-capabilities','exec'),ns)
chain=next(n for n in ast.parse(sources[paths[2]]).body if isinstance(n,ast.ClassDef)and n.name=='ChainBase');methods=[copy.deepcopy(n)for n in chain.body if isinstance(n,ast.FunctionDef)and n.name in ['start_message_processing_status','finish_message_processing_status']]
message=next(n for n in ast.parse(sources[paths[3]]).body if isinstance(n,ast.ClassDef)and n.name=='MessageChain');methods +=[copy.deepcopy(n)for n in message.body if isinstance(n,ast.FunctionDef)and n.name=='_update_interaction_message_feedback']
future=ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)
exec(compile(ast.fix_missing_locations(ast.Module(body=[future,ast.ClassDef(name='Fixed',bases=[],keywords=[],decorator_list=[],body=methods)],type_ignores=[])),'fixed-web-message-guards','exec'),ns)
rows=[]
for channel in ['Web','Telegram']:
 calls=[];obj=ns['Fixed']();obj.run_module=lambda name,**kw:calls.append(name)or{};ch=ns['MessageChannel'][channel]
 start=obj.start_message_processing_status(ch,'owned','owned',text='/owned')
 obj.finish_message_processing_status(channel=ch,source='owned',userid='owned')
 rows.append(dict(channel=channel,processing_capability=ns['ChannelCapabilityManager'].supports_capability(ch,ns['ChannelCapability'].PROCESSING_STATUS),calls=calls,start=start))
obj=ns['Fixed']();calls=[];obj.edit_message=lambda **kw:calls.append(kw)
obj._update_interaction_message_feedback(channel=ns['MessageChannel'].Web,source='owned',original_message_id=None,original_chat_id=None,prompt='owned',selected_label='owned')
rows.append(dict(channel='Web',endpoint_message_ids=[None,None],feedback_calls=calls))
assert rows[0]['calls']==[] and rows[1]['calls']==['mark_message_processing_started','mark_message_processing_finished'] and rows[2]['feedback_calls']==[]
Path(__file__).with_name('module-web-guard-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'source_sha256':{p:hashlib.sha256(v).hexdigest()for p,v in sources.items()},'boundary':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print('fixed Web processing status guard prevents both module calls; absent Web message IDs prevent edit')
