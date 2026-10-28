"""Fixed source ordinary text/history/role semantics; owned SQLite and boundaries."""
import ast,asyncio,datetime,hashlib,json,pathlib,queue,sqlite3,time,types
from typing import *
root=pathlib.Path('/Users/accidia/projects/MoviePilot');paths=['app/helper/message.py','app/db/message_oper.py','app/db/models/message.py','app/api/endpoints/message.py','app/chain/message.py','app/chain/__init__.py'];sources={p:(root/p).read_text() for p in paths}
def execute(nodes,ns,filename):
 for n in nodes:
  if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)):n.decorator_list=[]
 module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])
 exec(compile(ast.fix_missing_locations(module),filename,'exec'),ns)
def methods(path,owner,names):
 tree=ast.parse(sources[path]);cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==owner);out=[n for n in cls.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names];assert len(out)==len(names);return out
class Cache(dict):
 def __init__(self,**kwargs):super().__init__()
 def set(self,key,value):self[key]=value
hns=dict(queue=queue,TTLCache=Cache,time=time,json=json)
hmethods=methods(paths[0],'MessageHelper',['__init__','_build_system_notification_key','_is_recent_system_notification','put','get'])
execute([ast.ClassDef(name='MessageHelper',bases=[],keywords=[],body=hmethods,decorator_list=[])],hns,'fixed/helper/message.py');Helper=hns['MessageHelper']
helper=Helper();helper.put('owned ignored',role='user');user_rejected=helper.get('user') is None
helper.put('owned system',role='system');helper.put('owned plugin',role='plugin');role_reads=[json.loads(helper.get(role)) for role in ['user','system']];assert [v['type'] for v in role_reads]==['system','plugin']
conn=sqlite3.connect(':memory:');conn.row_factory=sqlite3.Row;columns=['channel','source','mtype','title','text','image','link','userid','action','reg_time','note'];conn.execute('CREATE TABLE messages(id INTEGER PRIMARY KEY AUTOINCREMENT,'+','.join(c+' '+('INTEGER' if c=='action' else 'TEXT') for c in columns)+')')
class Column:
 def __init__(self,name):self.name=name
 def desc(self):return self.name+' DESC'
class Query:
 def __init__(self,cls):self.cls=cls;self.order=[];self.start=0;self.count=-1
 def order_by(self,*columns):self.order=list(columns);return self
 def offset(self,value):self.start=value;return self
 def limit(self,value):self.count=value;return self
class Model:
 reg_time=Column('reg_time');id=Column('id');__table__=types.SimpleNamespace(columns=types.SimpleNamespace(keys=lambda:['id',*columns]))
 def __init__(self,**fields):self.fields=fields
 def create_and_to_dict(self,db):
  fields={k:v for k,v in self.fields.items() if k in columns};fields['note']=json.dumps(fields.get('note',{}),ensure_ascii=False)
  keys=list(fields);cur=db.execute('INSERT INTO messages('+','.join(keys)+') VALUES('+','.join('?' for _ in keys)+')',[fields[k] for k in keys]);db.commit();row=db.execute('SELECT * FROM messages WHERE id=?',(cur.lastrowid,)).fetchone();self.fields=dict(row);self.fields['note']=json.loads(self.fields['note']);return self.fields
 def to_dict(self):return self.fields
mns=dict(select=lambda cls:Query(cls));execute(methods(paths[2],'Message',['async_list_by_page']),mns,'fixed/db/models/message.py');Model.async_list_by_page=classmethod(mns['async_list_by_page'])
class AsyncDB:
 async def execute(self,q):
  sql='SELECT * FROM messages ORDER BY '+','.join(q.order)+' LIMIT ? OFFSET ?';rows=conn.execute(sql,(q.count,q.start)).fetchall();models=[]
  for row in rows:
   fields=dict(row);fields['note']=json.loads(fields['note']);models.append(Model(**fields))
  return types.SimpleNamespace(scalars=lambda:types.SimpleNamespace(all=lambda:models))
ons=dict(Message=Model,time=time);execute([ast.ClassDef(name='MessageOper',bases=[],keywords=[],body=methods(paths[1],'MessageOper',['add','async_list_by_page']),decorator_list=[])],ons,'fixed/db/message_oper.py');Oper=ons['MessageOper'];Oper.__init__=lambda self,db=None:setattr(self,'_db',db or conn)
class Comming(types.SimpleNamespace):
 MessageImage=types.SimpleNamespace(normalize_list=lambda values:values or [])
ns=dict(CommingMessage=Comming,datetime=datetime.datetime,MessageChannel=types.SimpleNamespace(Web=types.SimpleNamespace(value='Web')))
execute(methods(paths[4],'MessageChain',['_record_user_message','handle_message']),ns,'fixed/chain/message.py')
observations=[]
for name,consumed,text in [('ordinary',False,'/owned alpha'),('input_consumed',True,'owned reply'),('callback',False,'CALLBACK:owned')]:
 before=conn.execute('SELECT count(*) FROM messages').fetchone()[0];h=Helper()
 owner=types.SimpleNamespace(messagehelper=h,messageoper=Oper(conn),_handle_plugin_input_interaction=lambda **kw:consumed,_is_agent_message=lambda **kw:False,_mark_message_processing_started=lambda **kw:None,_handle_message_core=lambda **kw:False,_mark_message_processing_finished=lambda *a,**kw:None)
 owner._record_user_message=lambda **kw:ns['_record_user_message'](owner,**kw)
 ns['handle_message'](owner,channel=types.SimpleNamespace(value='Web'),source='owned-admin',userid='owned-admin',username='owned-admin',text=text)
 after=conn.execute('SELECT count(*) FROM messages').fetchone()[0]
 observations.append(dict(name=name,inserted=after-before,user_queue_empty=h.get('user') is None))
# Execute the source history endpoint and original query method with owned DB.
api=next(n for n in ast.parse(sources[paths[3]]).body if isinstance(n,ast.AsyncFunctionDef) and n.name=='get_web_message')
ans=dict(MessageOper=Oper,Depends=lambda *a:None,verify_token=None,get_async_db=None,logger=types.SimpleNamespace(error=lambda *a:None));execute([api],ans,'fixed/api/endpoints/message.py')
for i in range(24):Oper(conn).add(channel=types.SimpleNamespace(value='Web'),source='owned-admin',userid='owned-admin',text='owned history '+str(i),action=i%2)
# Equal reg_time forces the source id DESC tie-break to distinguish reversal.
conn.execute("UPDATE messages SET reg_time='2026-10-27 01:00:00'")
history=[]
for page,count in [(1,20),(2,20),(2,1),(0,1),(1,0),(1,-1)]:
 rows=asyncio.run(ans['get_web_message'](db=AsyncDB(),page=page,count=count));history.append(dict(page=page,count=count,ids=[r['id'] for r in rows]))
assert history[0]['ids']==list(range(25,5,-1))
# Prior processes may have used different local zones. The fixed model sorts
# persisted local reg_time, even when that order differs from the UTC instants.
timezone_samples=[dict(id=26,utc='2027-01-01T01:00:00Z',reg_time='2027-01-01 14:00:00'),dict(id=27,utc='2027-01-01T02:00:00Z',reg_time='2027-01-01 03:00:00')]
for sample in timezone_samples:
 conn.execute('INSERT INTO messages(id,channel,reg_time,note) VALUES(?,?,?,?)',(sample['id'],'Web',sample['reg_time'],'{}'))
timezone_history=asyncio.run(ans['get_web_message'](db=AsyncDB(),page=1,count=2));assert [r['id'] for r in timezone_history]==[26,27]
# Source rendering guard: save_history=False never calls MessageOper.add.
post=methods(paths[5],'ChainBase',['post_message'])[0];pns=dict(datetime=datetime.datetime,MessageTemplateHelper=types.SimpleNamespace(render=lambda **kw:kw['message']),EventType=types.SimpleNamespace(NoticeMessage='notice.message'),logger=types.SimpleNamespace(warning=lambda *a:None))
execute([post],pns,'fixed/chain/__init__.py');saves=[];notices=[];sends=[]
message=types.SimpleNamespace(save_history=False,userid='owned-admin',mtype=None)
owner=types.SimpleNamespace(messageoper=types.SimpleNamespace(add=lambda **kw:saves.append(kw)),_normalize_notification_for_dispatch=lambda m:m,_build_notice_message_data=lambda m:{'owned':'prompt'},eventmanager=types.SimpleNamespace(send_event=lambda **kw:notices.append(kw)),messagequeue=types.SimpleNamespace(send_message=lambda *a,**kw:sends.append('post_message')))
pns['post_message'](owner,message);assert saves==[] and len(notices)==1 and sends==['post_message']
result=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',source_sha256={p:hashlib.sha256(s.encode()).hexdigest() for p,s in sources.items()},kind='actual fixed MessageHelper/MessageChain ordinary wrapper/MessageOper/model query/history endpoint/post_message guard AST; SQLite/temp notification/provider boundaries owned, no old application/service imported',role_user_put_rejected=user_rejected,role_get_is_shared_fifo=[r['type'] for r in role_reads],ordinary_ingress=observations,history=history,timezone_history={'samples':timezone_samples,'ids':[r['id'] for r in timezone_history]},endpoint_defaults={'page':1,'count':20,'size_is_declared':False},save_history_false={'saved_rows':len(saves),'notice_events':len(notices),'queued_post_message':len(sends),'user_sse_message':False})
pathlib.Path(__file__).with_name('source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print('ordinary web/history/source defect replay passed')
