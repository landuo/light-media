"""Read-only fixed _normalize_plugin_label AST; developer replay, no app import."""
import ast,hashlib,json,subprocess,sys
from pathlib import Path
commit='8a83efeacd3e26b82be495e17d50146e31c96a60';path='app/core/plugin.py'
raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+path])
method=next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='_normalize_plugin_label');method.decorator_list=[]
module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),method],type_ignores=[])
ns={};exec(compile(ast.fix_missing_locations(module),path,'exec'),ns)
values=[None,'','  ',' 工具 ','\u001c\u001d test\u001e\u001f',[],[' 工具 ','','开发 ','\u001c'],[None,True,False,0,1,1.0,-0.0,1e20,1e-5,1e-4,900719925474099312345],['a\nb',{'z':"a'b\n",'a':[1,None]},['x',True]],{},True,42]
Path(__file__).with_name('labels-oracle.json').write_text(json.dumps({'source_commit':commit,'path':path,'sha256':hashlib.sha256(raw).hexdigest(),'kind':'actual fixed Python AST; owned JSON inputs; not runtime/browser proof','cases':[{'input':v,'expected':ns['_normalize_plugin_label'](v)} for v in values]},ensure_ascii=False,indent=2)+'\n')
