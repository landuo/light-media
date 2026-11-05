"""Fixed ServiceBaseHelper discovery oracle, isolated retained fake instances."""
import ast,hashlib,json,pathlib,subprocess,types
root=pathlib.Path(__file__).resolve().parent;commit='8a83efeacd3e26b82be495e17d50146e31c96a60';path='app/helper/service.py';raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',commit+':'+path]);tree=ast.parse(raw);cls=next(x for x in tree.body if isinstance(x,ast.ClassDef) and x.name=='ServiceBaseHelper');symbols={m.name:m.lineno for m in cls.body if isinstance(m,ast.FunctionDef)}
cls.bases=[]
for node in ast.walk(cls):
 if isinstance(node,ast.arg):node.annotation=None
 if isinstance(node,ast.FunctionDef):node.returns=None
configs=[types.SimpleNamespace(name='enabled',type='qbittorrent',enabled=True),types.SimpleNamespace(name='disabled',type='qbittorrent',enabled=False)]
namespace={'ServiceConfigHelper':types.SimpleNamespace(get_configs=lambda *_:configs),'ServiceInfo':types.SimpleNamespace}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),path,'exec'),namespace)
helper=object.__new__(namespace['ServiceBaseHelper']);helper.config_key=None;helper.conf_type=None;helper.module_type=None;helper.modulemanager=types.SimpleNamespace(get_running_type_modules=lambda *_:[types.SimpleNamespace(get_instances=lambda:{'enabled':object(),'disabled':object()})])
result={'source_commit':commit,'path':path,'sha256':hashlib.sha256(raw).hexdigest(),'symbols':symbols,'classification':'fixed source AST execution with synthetic config/retained module instances','default_configs':list(helper.get_configs()),'including_disabled':list(helper.get_configs(include_disabled=True)),'explicit_enabled_service':helper.get_service('enabled') is not None,'explicit_disabled_service':helper.get_service('disabled') is not None}
assert result['default_configs']==['enabled'] and result['including_disabled']==['enabled','disabled'] and result['explicit_enabled_service'] and not result['explicit_disabled_service']
(root/'disabled-source-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'status':'passed','explicit_disabled_service':False}))
