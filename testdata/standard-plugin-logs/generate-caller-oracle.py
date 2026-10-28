"""Run the fixed caller walk against synthetic frames, never real user paths."""
import ast,hashlib,json,pathlib,sys,types
p=pathlib.Path(sys.argv[1] if len(sys.argv)>1 else '/Users/accidia/projects/MoviePilot/app/log.py')
text=p.read_text();tree=ast.parse(text);node=next(n for n in ast.walk(tree) if isinstance(n,ast.FunctionDef) and n.name=='__get_caller');node.decorator_list=[]
cases=[]
for name,paths,failed in [('synchronous-host-helper',['/app/app/helper/message.py','/app/app/chain/__init__.py','/app/app/plugins/ownednotice/__init__.py'],False),('clone-host-helper',['/app/app/helper/message.py','/app/app/plugins/ownednoticelogs/__init__.py'],False),('background-host',['/app/app/helper/message.py','/app/app/main.py'],False),('outside-program-stops-walk',['/stdlib/threading.py','/app/app/plugins/ownednotice/__init__.py'],False),('unavailable-frame',[],True)]:
 frame=None
 for path in paths[::-1]:frame=types.SimpleNamespace(f_code=types.SimpleNamespace(co_filename=path),f_back=frame)
 def getframe(depth):
  assert depth==3
  if failed:raise ValueError('owned unavailable frame')
  return frame
 ns={'Path':pathlib.Path,'sys':types.SimpleNamespace(_getframe=getframe)}
 exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'<fixed log caller>','exec'),ns)
 cases.append(dict(name=name,frames=paths,frame_failure=failed,expected=ns['__get_caller']()))
output=dict(source=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',path='app/log.py',sha256=hashlib.sha256(p.read_bytes()).hexdigest(),function='LoggerManager.__get_caller'),kind='fixed source walk with synthetic frames; native Go context is a process-boundary adaptation',cases=cases)
pathlib.Path(__file__).with_name('caller-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');print('fixed caller cases',len(cases))
