"""Fixed ordinary MessageChain fallback with absent business interactions."""
import ast,hashlib,json,re
from pathlib import Path
from types import SimpleNamespace
path=Path('/Users/accidia/projects/MoviePilot/app/chain/message.py')
cls=next(x for x in ast.parse(path.read_text()).body if isinstance(x,ast.ClassDef) and x.name=='MessageChain')
methods=[x for x in cls.body if isinstance(x,ast.FunctionDef) and x.name in ['_handle_message_core','_strip_no_ai_prefix','_has_ai_prefix']]
for method in methods:
    method.returns=None
    for arg in method.args.args+method.args.kwonlyargs:arg.annotation=None
ns=dict(re=re,EventType=SimpleNamespace(UserMessage='user.message',CommandExcute='command.excute'),settings=SimpleNamespace(AI_AGENT_ENABLE=False,AI_AGENT_GLOBAL=False),media_interaction_manager=SimpleNamespace(get_by_user=lambda user:None),MediaInteractionChain=lambda:SimpleNamespace(handle_text_interaction=lambda **kwargs:False))
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='MessageChain',bases=[],keywords=[],body=methods,decorator_list=[])],type_ignores=[])),'fixed-ordinary-user-message','exec'),ns)
Chain=ns['MessageChain'];Chain._no_ai_prefix='/noai';Chain._ai_prefix='/ai';Chain._handle_plugin_input_interaction=lambda self,**kwargs:False;Chain._get_latest_slash_interaction=lambda self,user:None
cases=[]
for text,images in [(' ordinary \x1cmessage ',None),('after failure',None),(' /noai： ordinary escaped ',None),('ordinary image text',[{'url':'https://owned.invalid/unused.jpg'}])]:
    obj=Chain();events=[];obj.eventmanager=SimpleNamespace(send_event=lambda kind,data:events.append({'type':kind,'data':data}))
    result=obj._handle_message_core(channel='Web',source='admin',userid='admin',username='admin',text=text,images=images)
    cases.append({'text':text,'images':images,'events':events,'return':result})
print(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'source':'app/chain/message.py:_handle_message_core','kind':'actual fixed method bodies; no pending/business interaction and AI off dependency values; not original app runtime','cases':cases},ensure_ascii=False,indent=2))
