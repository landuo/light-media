#!/usr/bin/env python3
"""Execute fixed ordinary MessageChain AST with controlled modules/consumers.

No production imports, network, account data, AI or workflow execution.
"""
import ast, dataclasses, enum, hashlib, json, pathlib, re, subprocess, types

ROOT = pathlib.Path(__file__).resolve().parent
REPO = '/Users/accidia/projects/MoviePilot'
SHA = '8a83efeacd3e26b82be495e17d50146e31c96a60'

def source(path):
    return subprocess.check_output(['git', '-C', REPO, 'show', SHA + ':' + path])

def strip(node):
    for child in ast.walk(node):
        if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
            child.returns = None
        if isinstance(child, ast.arg):
            child.annotation = None
    return node

def normalize(value):
    if isinstance(value, enum.Enum): return value.value
    if isinstance(value, dict): return {key: normalize(val) for key, val in value.items()}
    if isinstance(value, (list, tuple)): return [normalize(val) for val in value]
    if dataclasses.is_dataclass(value): return normalize(dataclasses.asdict(value))
    return value

raw = source('app/chain/message.py')
tree = ast.parse(raw)
cls = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == 'MessageChain')
symbols = ['process', 'handle_message', '_handle_message_core', '_handle_callback', '_strip_no_ai_prefix', '_has_ai_prefix', '_record_user_message', '_mark_message_processing_started', '_mark_message_processing_finished']
nodes = [strip(node) for node in cls.body if isinstance(node, ast.FunctionDef) and node.name in symbols]
status_node = next(node for node in cls.body if isinstance(node, ast.ClassDef) and node.name == '_ProcessingStatus')
channels = enum.Enum('MessageChannel', {'Feishu':'飞书', 'Telegram':'Telegram', 'Web':'Web', 'QQ':'QQ'})
events = enum.Enum('EventType', {'CommandExcute':'command.excute', 'UserMessage':'user.message', 'MessageAction':'message.action'})
log = types.SimpleNamespace(info=lambda *args:None, debug=lambda *args:None, warning=lambda *args:None, error=lambda *args:None)
no_interaction = types.SimpleNamespace(handle_callback_interaction=lambda **_:False, handle_text_interaction=lambda **_:False)
namespace = dict(re=re, dataclass=dataclasses.dataclass, MessageChannel=channels, Optional=__import__('typing').Optional, Union=__import__('typing').Union, Dict=__import__('typing').Dict, Any=__import__('typing').Any, logger=log, EventType=events,
    Notification=lambda **kwargs:kwargs, CommingMessage=types.SimpleNamespace(MessageImage=types.SimpleNamespace(normalize_list=lambda values:values)),
    ChannelCapabilityManager=types.SimpleNamespace(supports_callbacks=lambda channel:channel in [channels.Feishu,channels.Telegram,channels.QQ]),
    SiteChain=lambda:no_interaction, SubscribeChain=lambda:no_interaction, SkillsChain=lambda:no_interaction, MediaInteractionChain=lambda:no_interaction,
    media_interaction_manager=types.SimpleNamespace(get_by_user=lambda _:None), settings=types.SimpleNamespace(AI_AGENT_ENABLE=False,AI_AGENT_GLOBAL=False))
namespace['CommingMessage'] = type('CommingMessage', (), {'__new__':lambda cls,**kwargs:kwargs,'MessageImage':namespace['CommingMessage'].MessageImage})
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Source',bases=[],keywords=[],body=[status_node]+nodes,decorator_list=[])],type_ignores=[])), 'app/chain/message.py', 'exec'), namespace)
Source = namespace['Source']
for node in cls.body:
    if isinstance(node, ast.Assign) and isinstance(node.targets[0],ast.Name) and node.targets[0].id in ['_ai_prefix','_no_ai_prefix']:
        setattr(Source,node.targets[0].id,ast.literal_eval(node.value))

def recorder(trace, kind):
    return lambda *args, **kwargs:trace.append({'kind':kind, 'args':normalize(args), 'kwargs':normalize(kwargs)})

def instance(trace, pending=False, status=True):
    value = Source()
    value.messagehelper=types.SimpleNamespace(put=recorder(trace,'helper_history'))
    value.messageoper=types.SimpleNamespace(add=recorder(trace,'history'))
    value.eventmanager=types.SimpleNamespace(send_event=lambda event,data:trace.append({'kind':'event','type':event.value,'data':normalize(data)}))
    value.post_message=lambda notification:trace.append({'kind':'post','data':normalize(notification)})
    value._is_agent_message=lambda **_:False
    value._handle_plugin_input_interaction=lambda **kwargs:trace.append({'kind':'input','data':normalize(kwargs)}) or (pending.pop(0) if isinstance(pending,list) and pending else pending if isinstance(pending,bool) else False)
    value._get_latest_slash_interaction=lambda _:None
    value._handle_transfer_callback=lambda **_:False
    value._handle_agent_choice_callback=lambda **_:False
    value.start_message_processing_status=lambda **kwargs:trace.append({'kind':'start','data':normalize(kwargs)}) or ({'metadata':{'owned':True}} if status else None)
    value.finish_message_processing_status=lambda **kwargs:trace.append({'kind':'finish','data':normalize(kwargs)})
    return value

process_cases=[]
for name, override in [('ordinary',{}),('missing-user',{'userid':None}),('empty-user',{'userid':''}),('zero-user',{'userid':0,'username':None}),('numeric-name',{'username':0}),('blank-text',{'text':' \u001c\u3000 '}),('images-only',{'text':'','images':['owned://image']}),('audio-only',{'text':'','audio_refs':['owned://audio']}),('files-only',{'text':'','files':[{'ref':'owned://file'}]})]:
    trace=[]
    data=dict(userid='owned-id',username='',source='ParsedSource',channel=channels.Feishu,text='  ordinary message\u001c ',images=None,audio_refs=None,files=None,message_id=9007199254740993,chat_id='chat',reply_to_message_id=22)
    data.update(override);value=instance(trace);value.message_parser=lambda **kwargs:trace.append({'kind':'parser','kwargs':normalize(kwargs)}) or types.SimpleNamespace(**data)
    value.handle_message=lambda **kwargs:trace.append({'kind':'handle','kwargs':normalize(kwargs)})
    value.process(body=b'owned body', form={'x':'last'}, args={'source':'IncomingSource'})
    # Body is a bytes value; record its digest independently of JSON encoding.
    for row in trace:
        if row['kind']=='parser': row['kwargs']['body']={'sha256':hashlib.sha256(row['kwargs']['body']).hexdigest(),'size':len(row['kwargs']['body'])}
    process_cases.append({'name':name,'parsed':normalize(data),'trace':trace})

route_cases=[]
for name,text,pending,channel,status in [('ordinary','ordinary message',False,channels.Feishu,True),('command','/owned_inbound one two',False,channels.Feishu,True),('unknown-command','/owned_missing',False,channels.Feishu,False),('explicit-ordinary','/noai: /owned_inbound owned',False,channels.Feishu,True),('explicit-ordinary-prefix','/noai /ai ordinary text',False,channels.Feishu,True),('empty-explicit','/noai',False,channels.Feishu,True),('input-first','/owned_inbound',True,channels.Feishu,True),('callback','CALLBACK:[PLUGIN]OwnedHostMessage|button|rest',False,channels.Feishu,True),('callback-unsupported','CALLBACK:[PLUGIN]OwnedHostMessage|button',False,channels.Web,False),('malformed-callback','CALLBACK:[PLUGIN]OwnedHostMessage',False,channels.Feishu,True),('qq-callback','CALLBACK:[PLUGIN]OwnedHostMessage|button',False,channels.QQ,False)]:
    trace=[];value=instance(trace,pending,status);error=None
    try:value.handle_message(channel=channel,source='ParsedSource',userid=9007199254740993,username='owned-name',text=text,original_message_id='m1',original_chat_id='c1',reply_to_message_id=42)
    except Exception as exc:error=type(exc).__name__
    route_cases.append({'name':name,'text':text,'pending':pending,'channel':channel.value,'status':status,'trace':trace,'exception':error})

trace=[];value=instance(trace,[False,True],True)
value.handle_message(channel=channels.Feishu,source='ParsedSource',userid=9007199254740993,username='owned-name',text='/owned_inbound',original_message_id='m1',original_chat_id='c1',reply_to_message_id=42)
route_cases.append(dict(name='input-after-status',text='/owned_inbound',pending=False,pending_after_status=True,channel=channels.Feishu.value,status=True,trace=trace,exception=None))

sources={path:{'sha256':hashlib.sha256(source(path)).hexdigest()} for path in ['app/chain/message.py','app/chain/__init__.py','app/schemas/message.py','app/schemas/types.py','app/api/endpoints/message.py','app/command.py']}
base_tree=ast.parse(source('app/chain/__init__.py'));base_cls=next(node for node in base_tree.body if isinstance(node,ast.ClassDef) and node.name=='ChainBase')
base_finish=strip(next(node for node in base_cls.body if isinstance(node,ast.FunctionDef) and node.name=='finish_message_processing_status'))
namespace['ChannelCapability']=types.SimpleNamespace(PROCESSING_STATUS='processing_status')
namespace['ChannelCapabilityManager'].supports_capability=lambda channel,capability:channel in [channels.Feishu,channels.Telegram]
exec(compile(ast.fix_missing_locations(ast.Module(body=[base_finish],type_ignores=[])),'app/chain/__init__.py','exec'),namespace)
finish_cases=[]
for label,user,message,chat in [('different','status-user','status-message','status-chat'),('zero',0,0,0),('none',None,None,None)]:
    trace=[];value=instance(trace);value.finish_message_processing_status=types.MethodType(namespace['finish_message_processing_status'],value);value.run_module=lambda method,**kwargs:trace.append({'method':method,'kwargs':normalize(kwargs)})
    value._mark_message_processing_finished(channel=channels.Feishu,source='ParsedSource',userid=9007199254740993,status=Source._ProcessingStatus(channel=channels.Feishu,source='ParsedSource',userid=user,message_id=message,chat_id=chat,metadata={'owned':True}),original_message_id='original-message',original_chat_id='original-chat')
    finish_cases.append(dict(name=label,status_user=user,status_message=message,status_chat=chat,trace=trace))
trace=[];value=instance(trace);info=dict(userid=0,username=None,channel=channels.Feishu,source='ParsedSource',text='CALLBACK:bad',images=None,audio_refs=None,files=None,message_id='m1',chat_id='c1',reply_to_message_id=None)
value.message_parser=lambda **_:types.SimpleNamespace(**info);value.process(body=b'owned',form={},args={'source':'Owned'})
fallback_callback=dict(parsed=normalize(info),trace=trace)
result=dict(source_commit=SHA,classification='fixed AST process/ordinary routing with controlled plugin input/module/event/history; no source business imports, network or AI execution',sources=sources,symbol_lines={node.name:node.lineno for node in nodes},process_cases=process_cases,route_cases=route_cases,
    finish_cases=finish_cases,normalized_username_callback=fallback_callback,
    exclusions={'audio_transcription':'message.py1565+ first checks AgentCapabilityManager.is_audio_input_available and later invokes LLMHelper; model-specific transcription excluded; ordinary parser body and audio_refs remain transported.','model_routes':'_handle_ai_message/_is_agent_message model dispatch and agent callbacks excluded; no model/agent/workflow extension point added.','business_interactions':'site/subscribe/media/transfer built-in command flows are not reimplemented by the plugin inbound bridge; registered ordinary plugin commands and plugin callbacks are retained.'})
(ROOT/'source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'process_cases':len(process_cases),'route_cases':len(route_cases),'status':'captured'}))
