"""Normal fixed StringUtils import and direct pure comparison calls only.
No Manager/helper fixture, server, proxy, TLS trust or copied implementation.
"""
import sys, json, hashlib, pathlib, unicodedata, os
source, input_file, output = map(pathlib.Path, sys.argv[1:4])
network_attempts=[]
def audit(event, args):
    if event in ('socket.connect','socket.connect_ex','socket.getaddrinfo'):
        network_attempts.append(event)
        raise RuntimeError('This pure component witness does not permit networking')
sys.addaudithook(audit)
sys.path.insert(0,str(source))
from app.utils.string import StringUtils
original=pathlib.Path(sys.modules['app.utils.string'].__file__).resolve()
assert original == (source/'app/utils/string.py').resolve()
assert unicodedata.unidata_version=='15.0.0'
assert sys.get_int_max_str_digits()==4300
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
inputs=json.loads(input_file.read_text())
cases=[]
for c in inputs['cases']:
    gt=StringUtils.compare_version(c['left'],'>',c['right'])
    lt=StringUtils.compare_version(c['left'],'<',c['right'])
    eq=StringUtils.compare_version(c['left'],'==',c['right'])
    cases.append({'name':c['name'],'left':c['left'],'right':c['right'],'source_gt':gt,'source_lt':lt,'source_eq':eq,'comparison':1 if gt else -1 if lt else 0})
nd=[];extra=[];space=[]
for cp in range(sys.maxunicode+1):
    s=chr(cp)
    if s.isdecimal():nd.append([cp,unicodedata.decimal(s)])
    elif s.isdigit():extra.append(cp)
    if s.isspace():space.append(cp)
imports={}
for module in tuple(sys.modules.values()):
    p=getattr(module,'__file__',None)
    if p and pathlib.Path(p).is_file():
        p=pathlib.Path(p).resolve();imports[str(p)]=sha(p)
result={'boundary':__doc__,'source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','python':sys.version,'unicode':unicodedata.unidata_version,'int_max_str_digits':sys.get_int_max_str_digits(),'source_file_sha256':sha(original),'driver_sha256':sha(pathlib.Path(__file__)),'input_sha256':sha(input_file),'cases':cases,'decimal_map':nd,'digit_only':extra,'strip_spaces':space,'network_attempts':network_attempts,'imports':imports,'same_previous_observations':all(result==inputs[k]for k,result in [('cases',cases),('decimal_map',nd),('digit_only',extra),('strip_spaces',space)])}
output.write_text(json.dumps(result,ensure_ascii=True,indent=2)+'\n')
print(json.dumps({'case_count':len(cases),'imports':len(imports),'network_attempts':network_attempts,'same_previous_observations':result['same_previous_observations'],'result_sha256':sha(output)}))
assert result['same_previous_observations'] and not network_attempts
