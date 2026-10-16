import ast
import hashlib
import itertools
import json
import pathlib
import random
import string
import subprocess

ROOT=pathlib.Path(__file__).resolve().parent
legacy=json.loads((ROOT/'legacy-installed-source.json').read_text())
legacy_words=(ROOT/'legacy_syllables.txt').read_text().splitlines()
rust_words=(ROOT/'rust_syllables.txt').read_text().splitlines()
union=sorted(set(legacy_words)|set(rust_words))
special=['',' ','\t\n','---','!!!','ni hao','NI HAO','xian','xianan','xianxiang','zhonghuarenmin','zhongq','xxqz','woaini','woshizhongguoren','Chang An','Long','The','Synthetic','null','kei','jue','jve','eng','nü','lü','nǚ','nǜ','lv','lve','nve',"xi'an",'xi-an','ni3','123ni456','!ni!','中文','中文 ni 好','boKa','Ka','Kei','İ','İn','ni😀hao','你好ni','ni你好','中ni好','ni\x00hao','ni\x1chao','ni\u200bhao']
spaces=legacy['whitespace']+[0x200b,0x180e,0xfeff]
for cp in spaces:special.extend([chr(cp),'ni'+chr(cp)+'hao',chr(cp)+'ni'+chr(cp)])
syllables=list(dict.fromkeys(union+[v.upper() for v in union]+[p+v+s for v in union for p,s in [('!',''),('','!'),(' ',''),('',' ')]]+special))
names=list(dict.fromkeys(special+syllables+[a+b for a in rust_words for b in rust_words]+[''.join(v) for v in itertools.product(string.ascii_lowercase,repeat=3)]))
random.seed(1744)
segments=list(dict.fromkeys(['','xian','xianan','zhonghuarenmin','xxqz','zhongq','ü','nü','中文','A中','ß','é','İ','a😀']+rust_words+[v.upper() for v in rust_words]+[''.join(v) for v in itertools.product('aehinouxz',repeat=3)]+[a+b for a,b in random.sample(list(itertools.product(rust_words,rust_words)),1000)]))

remote=r'''import ast,hashlib,importlib.metadata,json,pathlib,typing
dist=importlib.metadata.distribution('Pinyin2Hanzi');assert dist.version=='0.1.1'
raw=pathlib.Path(dist.locate_file('Pinyin2Hanzi/util.py')).read_bytes();assert hashlib.sha256(raw).hexdigest()=='5e863ce8c401e27ce820fba275ac1f215b26c0f427906da793684b9c660212b7'
tree=ast.parse(raw)
nodes=[n for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='__pinyin' for t in n.targets) or isinstance(n,ast.FunctionDef) and n.name=='is_pinyin']
namespace={};exec(compile(ast.Module(body=nodes,type_ignores=[]),'<actual-installed-Pinyin2Hanzi-util>','exec'),namespace)
raw=pathlib.Path('/app/app/core/meta/metavideo.py').read_bytes();assert hashlib.sha256(raw).hexdigest()=='ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077'
method=next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='__is_pinyin');method.decorator_list=[]
namespace['Optional']=typing.Optional;exec(compile(ast.Module(body=[method],type_ignores=[]),'<actual-installed-MetaVideo-pinyin-wrapper>','exec'),namespace)
'''
payload={'syllables':syllables,'names':names}
remote+='\ninputs=json.loads('+repr(json.dumps(payload,ensure_ascii=True))+')\n'
remote+='print(json.dumps({"syllables":[namespace["is_pinyin"](v) for v in inputs["syllables"]],"names":[namespace["__is_pinyin"](v) for v in inputs["names"]]}))\n'
result=subprocess.run(['ssh','-C','-o','BatchMode=yes','admin@192.168.8.129','docker exec -i moviepilot python -'],input=remote,text=True,capture_output=True,check=True)
legacy_results=json.loads(result.stdout)
queries=[{'mode':'syllable','text':v} for v in syllables]+[{'mode':'name','text':v} for v in names]+[{'mode':'segment','text':v} for v in segments]
binary=ROOT/'rust_oracle/target/release/installed-pinyin-reference'
result=subprocess.run([str(binary)],input=''.join(json.dumps(q,ensure_ascii=False)+'\n' for q in queries),text=True,capture_output=True,check=True)
rust_results=[json.loads(line) for line in result.stdout.splitlines()]
assert len(rust_results)==len(queries)
offset=len(syllables)
output={'provenance':json.loads((ROOT/'source-checksums.json').read_text()),'legacy_runtime':'actual installed Python source AST executed on NAS with no application imports and synthetic strings','rust_runtime':'actual fixed crate compiled locally and exact committed parser wrapper text; catch_unwind records raw UTF8 slicing panics','syllables':[{'text':v,'legacy':legacy_results['syllables'][i],'rust':rust_results[i]['value']} for i,v in enumerate(syllables)],'names':[{'text':v,'legacy':legacy_results['names'][i],'rust':rust_results[offset+i]['value']} for i,v in enumerate(names)],'segments':[]}
offset+=len(names)
for i,v in enumerate(segments):output['segments'].append({'text':v,**rust_results[offset+i]})
(ROOT/'oracle.json').write_text(json.dumps(output,ensure_ascii=False,separators=(',',':'))+'\n')
print(json.dumps({'syllables':len(syllables),'names':len(names),'segments':len(segments),'segment_panics':sum(bool(v.get('panic')) for v in output['segments']),'sha256':hashlib.sha256((ROOT/'oracle.json').read_bytes()).hexdigest()}))
