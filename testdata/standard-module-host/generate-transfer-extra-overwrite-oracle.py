"""Execute the pinned native overwrite branch for independent auxiliary tasks.
Controlled classifier returns true for a selected subtitle/audio. The complete
source if/else is retained; no statement in the unused main branch is replaced.
"""
import ast,copy,json,subprocess
from pathlib import Path
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';source='app/modules/filemanager/transhandler.py'
text=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',fixed+':'+source],text=True)
tree=ast.parse(text)
branch=next(n for n in ast.walk(tree)if isinstance(n,ast.If)and isinstance(n.test,ast.UnaryOp)and ast.unparse(n.test)=='not __is_extra_file(fileitem)')
wrapper=ast.parse('def run():\n overflag=False\n return overflag').body[0];wrapper.body.insert(1,copy.deepcopy(branch))
module=ast.fix_missing_locations(ast.Module(body=[wrapper],type_ignores=[]));code=compile(module,'<fixed:'+source+':extra overwrite>','exec');cases=[]
for kind in ['subtitle','audio']:
 for policy in [None,'never','always','size','latest']:
  calls=[]
  def classifier(item):calls.append(item);return True
  ns={'__is_extra_file':classifier,'fileitem':kind,'overwrite_mode':policy,'overflag':False}
  exec(code,ns);ns['overflag']=ns['run']();assert ns['overflag'] is True and calls==[kind]
  cases.append({'kind':kind,'policy':policy,'overwrite':ns['overflag'],'classifier_calls':calls})
Path(__file__).with_name('transfer-extra-overwrite-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'source':source+':432-577','kind':__doc__,'cases':cases},indent=2)+'\n')
print(len(cases),'fixed independent auxiliary overwrite cases')
