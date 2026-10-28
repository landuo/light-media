"""Only fixed ordinary plugin input classes/method; no AI code is loaded."""
import ast, dataclasses, hashlib, json, threading, uuid
from datetime import datetime, timedelta
from enum import Enum
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Dict, List, Optional, Tuple, Union

root = Path('/Users/accidia/projects/MoviePilot')
paths = [root/'app/helper/interaction.py', root/'app/chain/message.py']
clock = [datetime(2026, 1, 1)]
class Clock(datetime):
    @classmethod
    def now(cls): return clock[0]
class Channel(Enum):
    Web = 'Web'
    Telegram = 'Telegram'
ns = dict(dataclass=dataclasses.dataclass, field=dataclasses.field,
          datetime=Clock, timedelta=timedelta, Lock=threading.Lock, uuid=uuid,
          MessageChannel=Channel, Union=Union, Optional=Optional, Any=Any,
          Dict=Dict, List=List, Tuple=Tuple)
tree = ast.parse(paths[0].read_text())
nodes = [x for x in tree.body if isinstance(x, ast.ClassDef) and x.name in ['PendingPluginInputInteraction','PluginInputInteractionManager']]
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[])), 'fixed-plugin-input-manager', 'exec'), ns)
tree = ast.parse(paths[1].read_text())
cls = next(x for x in tree.body if isinstance(x, ast.ClassDef) and x.name == 'MessageChain')
method = next(x for x in cls.body if isinstance(x, ast.FunctionDef) and x.name == '_handle_plugin_input_interaction')
for arg in method.args.args: arg.annotation = None
method.returns = None
chain_ns = dict(plugin_input_interaction_manager=None,
                EventType=SimpleNamespace(MessageAction='message.action'),
                Notification=lambda **kwargs: kwargs)
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='MessageChain',bases=[],keywords=[],body=[method],decorator_list=[])],type_ignores=[])), 'fixed-plugin-input-message', 'exec'), chain_ns)
cases=[]
for name,text,expired in [('active-slash','/owned literal',False),('unicode-cancel','\x1cQuIt\x1f',False),('unicode-empty','\x1c\x1f',False),('callback','CALLBACK:owned',False),('expired-slash','/owned',True),('expired-answer','answer',True)]:
    clock[0] = datetime(2026,1,1)
    manager=ns['PluginInputInteractionManager']()
    request=manager.create_or_replace(user_id='admin',plugin_id='OwnedInput',channel=Channel.Web,source='admin',username='admin',timeout_seconds=1,payload={'large':9007199254740993},prompt_id='owned')
    if expired: clock[0]+=timedelta(seconds=2)
    chain_ns['plugin_input_interaction_manager']=manager
    obj=chain_ns['MessageChain']();events=[];notifications=[]
    obj.eventmanager=SimpleNamespace(send_event=lambda kind,data:events.append({'type':kind,'data':data}))
    obj.post_message=lambda notification:notifications.append(notification)
    consumed=obj._handle_plugin_input_interaction(channel=Channel.Web,source='admin',userid='admin',username='admin',text=text)
    for event in events:
        event['data']['text']=event['data']['text'].replace(request.request_id,'<session>')
        event['data']['input_session_id']='<session>'
        event['data']['channel']=event['data']['channel'].value
    for notification in notifications: notification['channel']=notification['channel'].value
    cases.append({'name':name,'text':text,'expired':expired,'consumed':consumed,'events':events,'notifications':notifications,'pending':manager.get_by_user('admin',Channel.Web,'admin') is not None})
print(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed ordinary input dataclass/manager and MessageChain method; controlled clock, captured event/notice dependencies; no original app runtime', 'source_sha256':{str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths},'cases':cases},ensure_ascii=False,indent=2))
