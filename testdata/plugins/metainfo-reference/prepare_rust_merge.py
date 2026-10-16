"""Compile exact fixed Rust merge functions on synthetic MetaResult pairs only."""
from pathlib import Path
import re,json,hashlib,subprocess
import argparse
a=argparse.ArgumentParser();a.add_argument('--source',type=Path,required=True);a.add_argument('--output-dir',type=Path,required=True);args=a.parse_args();src=args.source.resolve();out=args.output_dir.resolve();out.mkdir(parents=True,exist_ok=True);parser=(src/'parser.rs').read_text();model=(src/'model.rs').read_text()
def function(name):
 m=re.search(r'(?:pub\(super\) )?fn '+name+r'\(',parser);start=m.start();brace=parser.index('{',start);depth=1;i=brace+1
 while depth:
  if parser[i]=='{':depth+=1
  elif parser[i]=='}':depth-=1
  i+=1
 return parser[start:i].replace('pub(super) ','')
model=model[:model.index('\npub(crate) struct ExplicitMetaInfo')]
fields=re.findall(r'pub\(crate\) (\w+): ([^,]+),',model)
def rust_string(s):return json.dumps(s,ensure_ascii=False).replace('\\u0000','\\0')
def literal(k,v):
 t=dict(fields)[k]
 if t=='String':return rust_string(v)+'.to_string()'
 if t=='bool':return str(v).lower()
 if t=='i64':return str(v)
 if t=='Vec<String>':return 'vec!['+','.join(rust_string(x)+'.to_string()' for x in v)+']'
 if v is None:return 'None'
 if t=='Option<String>':return 'Some('+rust_string(v)+'.to_string())'
 return 'Some('+str(v)+')'
string_keys=[k for k,t in fields if t=='Option<String>'];num_keys=[k for k,t in fields if t=='Option<i64>']
pairs=[]
source={k:'Source' for k in string_keys};source.update({k:24 for k in num_keys});source.update(media_type='电视剧',cn_name='来源',en_name='Source',begin_season=2,end_season=3,total_season=2,begin_episode=4,end_episode=6,total_episode=3,kind='anime',title='Source title',org_string='Source raw',subtitle='Source sub',isfile=True,apply_words=['Source => Target'],subtitle_flag=True)
for value in [None,'','Target']:
 for num in [None,0,1]:
  for cn,en in [(None,None),('',None),('','English'),('混合1',''),('中文',None),(None,'English')]:
   for typ in ['未知','电影','电视剧']:
    target={k:value for k in string_keys};target.update({k:num for k in num_keys});target.update(media_type=typ,cn_name=cn,en_name=en,kind='video',title='Target title',org_string='Target raw',subtitle='Target sub',isfile=False,apply_words=[],total_season=0,total_episode=0,subtitle_flag=False)
    pairs.append(dict(name='merge/'+str(len(pairs)),target=target,source=source))
lines=['const MEDIA_TYPE_UNKNOWN:&str="未知";const MEDIA_TYPE_TV:&str="电视剧";',model]+[function(n) for n in ['merge_meta','fill_option','meta_name','is_all_chinese','is_chinese_char']]
# A tiny unambiguous typed stream, not a reimplementation of the merge method.
lines.append('fn string(s:&str)->String{s.as_bytes().iter().map(|b|format!("{:02x}",b)).collect()}')
lines.append('fn emit(v:&MetaResult){')
for k,t in fields:
 if t=='String':statement='format!("s{}",string(&v.'+k+'))'
 elif t=='Option<String>':statement='v.'+k+'.as_ref().map(|s|format!("s{}",string(s))).unwrap_or("n".to_string())'
 elif t=='Option<i64>':statement='v.'+k+'.map(|n|format!("i{}",n)).unwrap_or("n".to_string())'
 elif t=='i64':statement='format!("i{}",v.'+k+')'
 elif t=='bool':statement='format!("b{}",v.'+k+')'
 else:continue
 lines.append('println!("'+k+'\\t{}",'+statement+');')
lines.append('println!("END");}')
lines.append('fn main(){')
for p in pairs:
 for name,obj in [('target',p['target']),('source',p['source'])]:lines.append('let mut '+name+'=MetaResult{'+','.join(k+':'+literal(k,v) for k,v in obj.items())+',..MetaResult::default()};')
 lines.append('merge_meta(&mut target,&source);emit(&target);')
lines.append('}')
(out/'rust_merge.rs').write_text('\n'.join(lines)+'\n')
subprocess.run(['rustc','--edition=2021',str(out/'rust_merge.rs'),'-o',str(out/'rust_merge')],check=True,stderr=subprocess.PIPE)
output=subprocess.check_output([str(out/'rust_merge')],text=True);states=[]
for group in output.split('END\n')[:-1]:
 state={}
 for line in group.splitlines():
  k,v=line.split('\t');state[k]=None if v=='n' else bytes.fromhex(v[1:]).decode() if v[0]=='s' else int(v[1:]) if v[0]=='i' else v[1:]=='true'
 states.append(state)
assert len(states)==len(pairs)
for pair,state in zip(pairs,states):pair['result']=state
(out/'rust-merge-oracle.json').write_text(json.dumps(dict(source='exact fixed parser.rs functions and actual MetaResult compiled with synthetic literal pairs',sha256={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in [src/'parser.rs',src/'model.rs']},compiler=subprocess.check_output(['rustc','--version'],text=True).strip(),cases=pairs),ensure_ascii=False,indent=2)+'\n')
print(len(pairs))
