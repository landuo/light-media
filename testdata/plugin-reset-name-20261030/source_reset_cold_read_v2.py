"""Fresh original default DAO process on the same owned SQLite, no host/auth claim."""
import json,socket
from pathlib import Path
import sys,hashlib

def deny(*args,**kwargs):raise RuntimeError('owned cold probe forbids network')
socket.socket.connect=deny;socket.socket.connect_ex=deny;socket.create_connection=deny
from app.db.systemconfig_oper import SystemConfigOper
from app.db.plugindata_oper import PluginDataOper
from app.db import Engine
config=SystemConfigOper();data=PluginDataOper()
value={pid:{'config':config.get('plugin.'+pid),'data':data.get_data(pid,'marker')} for pid in ('OwnedResetMutator','OwnedResetObserver')}
print('OWNED_RESULT='+json.dumps(value,ensure_ascii=False))
Engine.dispose()
