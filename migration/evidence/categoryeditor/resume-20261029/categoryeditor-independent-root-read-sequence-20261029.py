#!/usr/bin/env python3
"""Source-only bad-root/read-IO sequence; owned files and explicit adapters."""
from pathlib import Path
import hashlib,json,sys,tempfile,types
ROOT=Path(__file__).resolve().parent.parent;E=ROOT/'evidence';sys.dont_write_bytecode=True
sys.path.insert(0,str(ROOT.parent/'toolchain/python-oracle/ruamel-0.19.1'))
import ruamel.yaml
assert ruamel.yaml.__version__=='0.19.1'
H=ROOT/'light-media/testdata/plugins/category-editor-1.3/installed-category-helper.py';P=ROOT/'source-snapshot/installed-plugin-sources/categoryeditor/__init__.py'
assert hashlib.sha256(H.read_bytes()).hexdigest()=='1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2'
assert hashlib.sha256(P.read_bytes()).hexdigest()=='24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27'
fixture=Path(tempfile.mkdtemp(prefix='categoryeditor-independent-root-read-sequence-',dir=E)); config=fixture/'config';inner=fixture/'inner';config.mkdir();inner.mkdir();user=config/'category.yaml'
initial="movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n";user.write_text(initial);(inner/'category.yaml').write_text(initial)
logs=[];trace=[]
for name in ['app','app.core','app.schemas','app.utils','app.modules']:
 m=types.ModuleType(name);m.__path__=[];sys.modules[name]=m
for name,attrs in [('app.core.config',{'settings':types.SimpleNamespace(CONFIG_PATH=config,INNER_CONFIG_PATH=inner)}),('app.log',{'logger':types.SimpleNamespace(**{level:lambda *a,level=level,**k:logs.append({'level':level,'args':[str(x).replace(str(fixture),'$CASE') for x in a]}) for level in ['warn','info','error']})}),('app.schemas.category',{'CategoryConfig':type('Unused',(),{})}),('app.utils.singleton',{'WeakSingleton':type})]:
 m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m
hmod=types.ModuleType('source_helper');exec(compile(H.read_bytes(),str(H),'exec'),hmod.__dict__);h=hmod.CategoryHelper()
class Base:
 def __init__(self):self.systemmessage=types.SimpleNamespace(put=lambda *a,**k:trace.append({'event':'put','args':a,'kwargs':k}))
 def update_config(self,c):trace.append({'event':'update_config','config':c});return True
for name,attrs in [('app.plugins',{'_PluginBase':Base}),('app.modules.themoviedb',{'CategoryHelper':lambda:h})]:
 m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m
pmod=types.ModuleType('source_plugin');exec(compile(P.read_bytes(),str(P),'exec'),pmod.__dict__);p=pmod.CategoryEditor()
steps=[]
for name,content,mode in [('truthy_root_failure','42\n',0o600),('read_failure_after_bad_root','movie:\n  New:\n',0o200),('parser_failure_clears_bad_root','movie: [\n',0o600),('read_failure_after_parser_failure','movie:\n  New:\n',0o200)]:
 user.chmod(mode);b=len(trace);l=len(logs);row={'id':name,'input_content':content,'mode':oct(mode)}
 try:row['return']=p.init_plugin({'enabled':True,'content':content})
 except Exception as x:row['exception']={'type':type(x).__name__,'message':str(x).replace(str(fixture),'$CASE')}
 row.update({'enabled':p.get_state(),'trace':trace[b:],'logs':logs[l:],'root_type':type(h._categorys).__name__,'movie_categories':h.movie_categorys,'movie_match':h.get_movie_category({'original_language':'zh'})});steps.append(row)
user.chmod(0o600)
out={'scope':'original historical helper / current uploaded plugin, controlled whole-module replay, real owned filesystem; no current NAS claim','source_sha256':{str(x.relative_to(ROOT)):hashlib.sha256(x.read_bytes()).hexdigest() for x in [H,P,Path(__file__)]},'fixture':str(fixture.relative_to(ROOT)),'steps':steps}
p=E/'categoryeditor-independent-root-read-sequence-20261029.json';p.write_text(json.dumps(out,ensure_ascii=True,indent=2)+'\n');print(json.dumps(out,ensure_ascii=False,indent=2))
