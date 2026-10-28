"""Pinned Notification/base post_message/MessageOper note handling, no old service."""
import ast,hashlib,json,pathlib,time,types
from enum import Enum
from typing import *
import pydantic
from pydantic import BaseModel
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
paths=['app/schemas/types.py','app/schemas/message.py','app/plugins/__init__.py','app/db/message_oper.py','app/db/models/message.py']
raw={p:(root/p).read_text() for p in paths}
ns=dict(Enum=Enum,BaseModel=BaseModel,Optional=Optional,Union=Union,List=List,Dict=Dict,Any=Any,__name__='fixed_notification_note')
def execute(nodes,filename):
 module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])
 exec(compile(ast.fix_missing_locations(module),filename,'exec'),ns)
types_tree=ast.parse(raw[paths[0]])
execute([n for n in types_tree.body if isinstance(n,ast.ClassDef) and n.name in ['MessageChannel','NotificationType','ContentType']],'fixed/types.py')
schema_tree=ast.parse(raw[paths[1]])
execute([n for n in schema_tree.body if isinstance(n,ast.ClassDef) and n.name in ['Notification','NotificationHistoryItem']],'fixed/message.py')
Notification=ns['Notification'];History=ns['NotificationHistoryItem'];Notification.model_rebuild(_types_namespace=ns);History.model_rebuild(_types_namespace=ns)
base_tree=ast.parse(raw[paths[2]]);post=next(n for n in ast.walk(base_tree) if isinstance(n,ast.FunctionDef) and n.name=='post_message');post.decorator_list=[]
ns['settings']=types.SimpleNamespace(MP_DOMAIN=lambda route:'http://owned.invalid/'+route);execute([post],'fixed/plugin_base.py')
input_note=[dict(title_year='Owned note (2026)',type='电影',vote_average=8.5)];captured=[]
owner=types.SimpleNamespace(chain=types.SimpleNamespace(post_message=lambda msg:captured.append(msg)))
ns['post_message'](owner,channel=ns['MessageChannel'].Web,title='Owned ordinary cover',text='**Owned markdown**',image='data:image/svg+xml,owned',link='http://owned.invalid/#plugins',note=input_note)
assert len(captured)==1 and 'note' not in captured[0].model_dump()
model_tree=ast.parse(raw[paths[4]]);model=next(n for n in model_tree.body if isinstance(n,ast.ClassDef) and n.name=='Message')
columns=[n.targets[0].id for n in model.body if isinstance(n,ast.Assign) and isinstance(n.targets[0],ast.Name) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Name) and n.value.func.id=='Column']
class OwnedMessage:
 __table__=types.SimpleNamespace(columns=types.SimpleNamespace(keys=lambda:columns))
 def __init__(self,**fields):self.fields=fields
 def create_and_to_dict(self,db):return self.fields
ns.update(Message=OwnedMessage,time=time)
oper_tree=ast.parse(raw[paths[3]]);add=next(n for n in ast.walk(oper_tree) if isinstance(n,ast.FunctionDef) and n.name=='add');add.decorator_list=[];execute([add],'fixed/message_oper.py')
history=ns['add'](types.SimpleNamespace(_db=None),**captured[0].model_dump());assert history['note']=={}
result=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',pydantic=pydantic.__version__,kind='actual pinned enum/Notification/base post_message/MessageOper.add AST; owned chain collector and DB insert boundary; no source service/app imported',source_sha256={p:hashlib.sha256(s.encode()).hexdigest() for p,s in raw.items()},input_note=input_note,notification_fields=list(Notification.model_fields),history_dto_accepts_note=History(note=input_note).model_dump(mode='json')['note'],notification_ignores_note='note' not in captured[0].model_dump(),history_note=history['note'],source_extra_config=str(Notification.model_config),note_producer='Other ordinary MessageChain result/history paths may supply note; do not add it to Notification or invent a new builtin business host')
pathlib.Path(__file__).with_name('note-source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print('fixed Notification/base/MessageOper ignores note; separate history DTO accepts note')
