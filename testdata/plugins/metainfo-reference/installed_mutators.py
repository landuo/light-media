"""Read-only installed-source reference; run inside the existing MoviePilot container.

No application package is imported. AST modules receive synthetic configuration and
safe class dependencies only. Third-party parsers are the installed distributions.
The generated fixture contains synthetic release titles and no user state/secrets.
"""
import ast
import hashlib
import importlib.metadata
import json
import pathlib
import sys
import types
from enum import Enum
from types import SimpleNamespace as NS
from typing import Optional

ROOT = pathlib.Path('/app')
EXPECTED = {
 'app/core/metainfo.py': '7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9',
 'app/core/meta/metavideo.py': 'ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077',
 'app/core/meta/metaanime.py': 'd45433c77656bba3203326943c9fe5b3ae6cbe8c7514e631fb16cc7dc1f4afca',
 'app/utils/tokens.py': '6b1547583adae77bffd2002660dd718ad60929926bf814e548741deb8aaea8ec',
 'app/utils/string.py': 'b21856bf4b309d20fe9d5a68538fbb0ddc7a2c23bbde424e5700caf57587297a',
}
EXPECTED.update({'app/schemas/types.py': '025060b9dd29be0fc811cb42ea357e7cd23ce1f450cb704f04f18012ed8be5c1', 'app/utils/singleton.py': '36453247c99d0acd190956b72e1f566c3c3c2250ec310eb680f1ea882cd2fec4', 'app/utils/string.py': 'b21856bf4b309d20fe9d5a68538fbb0ddc7a2c23bbde424e5700caf57587297a', 'app/utils/tokens.py': '6b1547583adae77bffd2002660dd718ad60929926bf814e548741deb8aaea8ec', 'app/utils/zhconv.py': 'f21b834f3c3d619697383e37ebe795517bdf5237ba8efa0f6ca0c22b6d8320ff', 'app/core/meta/releasegroup.py': 'adecb60bcfe3772d71fabbf1cbd45146e7577c9be1c22f1ae4af74ab771da13f', 'app/core/meta/customization.py': '6ff9692f267b6d5ec8eac5d2a47523fef22a55f4566f252c86e0595c2213ba67', 'app/core/meta/streamingplatform.py': '6aa9c8a21a6b2e257a8ab4cb5b99a535ab799378d58bcf95bfa6a3d326d6542a', 'app/core/meta/words.py': '798759c94919636c94b690786b9d68a8b7f2f2815be611b04f7c857491d40277', 'app/core/meta/metabase.py': '0156c105bf92991332a8742b9a4af4e1f3a74cf4b6f08107e0ed6de9d546f292', 'app/core/meta/metavideo.py': 'ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077', 'app/core/meta/metaanime.py': 'd45433c77656bba3203326943c9fe5b3ae6cbe8c7514e631fb16cc7dc1f4afca', 'app/core/meta/infopath.py': 'ac8719199132d8abdd571885e951317517bd7c5114f94eb5aac4070e99012880', 'app/utils/rust_accel.py': '6c4c890639c0830d41a75d2e865eb3b3c3f3915b234d72976ea58bfbe5dc9dfc', 'app/core/metainfo.py': '7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9'})
sha = {}
class NoApplicationImports(ast.NodeTransformer):
 def visit_ImportFrom(self, node):
  return None if node.module and (node.module == 'app' or node.module.startswith('app.')) else node
 def visit_Import(self, node):
  assert not any(x.name == 'app' or x.name.startswith('app.') for x in node.names)
  return node

def load(path, injected=None, class_only=None):
 source = (ROOT / path).read_bytes()
 sha[path] = hashlib.sha256(source).hexdigest()
 if path in EXPECTED:
  assert sha[path] == EXPECTED[path], ('installed-source changed', path, sha[path])
 tree = ast.parse(source)
 if class_only:
  tree.body = [next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == class_only)]
 tree = NoApplicationImports().visit(tree)
 name = 'release_meta_oracle_' + path.replace('/', '_').replace('.', '_')
 mod = types.ModuleType(name)
 sys.modules[name] = mod
 mod.__dict__.update(injected or {})
 exec(compile(ast.fix_missing_locations(tree), '<isolated installed ' + path + '>', 'exec'), mod.__dict__)
 return mod

class Logger:
 def __getattr__(self, key):
  return lambda *a, **kw: None
logger = Logger()
values = {}
class SyntheticSystemConfigOper:
 def get(self, key):
  return values.get(key)
keys = NS(CustomIdentifiers='words', Customization='customization', CustomReleaseGroups='release_groups')
settings = NS(RMT_MEDIAEXT=['.mp4','.mkv','.avi','.mov','.ts','.iso','.m2ts','.wmv','.flv'], RMT_SUBEXT=['.srt','.ass','.ssa','.sub','.vtt'], RMT_AUDIOEXT=['.mp3','.flac','.aac','.wav'], RUST_ACCEL=False)
media_type = load('app/schemas/types.py', {'Enum':Enum,'Optional':Optional}, 'MediaType').MediaType
singleton = load('app/utils/singleton.py').Singleton
base = {'logger':logger, 'MediaType':media_type, 'settings':settings, 'Singleton':singleton,
        'SystemConfigOper':SyntheticSystemConfigOper, 'SystemConfigKey':keys}
string = load('app/utils/string.py', base).StringUtils
base['StringUtils'] = string
base['Tokens'] = load('app/utils/tokens.py').Tokens
base['zhconv_convert'] = load('app/utils/zhconv.py').convert
base['ReleaseGroupsMatcher'] = load('app/core/meta/releasegroup.py', base).ReleaseGroupsMatcher
base['CustomizationMatcher'] = load('app/core/meta/customization.py', base).CustomizationMatcher
base['StreamingPlatforms'] = load('app/core/meta/streamingplatform.py', base).StreamingPlatforms
base['WordsMatcher'] = load('app/core/meta/words.py', base).WordsMatcher
base['MetaBase'] = load('app/core/meta/metabase.py', base).MetaBase
base['MetaVideo'] = load('app/core/meta/metavideo.py', base).MetaVideo
base['MetaAnime'] = load('app/core/meta/metaanime.py', base).MetaAnime
# Actual installed path helpers are loaded, although this oracle calls titles.
path_helpers=load('app/core/meta/infopath.py',base)
base.update(clear_parsed_title_for_parent_merge=path_helpers.clear_parsed_title_for_parent_merge,should_use_parent_title_for_file_stem=path_helpers.should_use_parent_title_for_file_stem)
runtime=load('app/utils/rust_accel.py',dict(settings=settings,logger=logger,log_settings=NS(DEBUG=False,LOG_LEVEL='INFO')))
base['rust_accel']=runtime
meta=load('app/core/metainfo.py',base)
import multiprocessing as mp
import anitopy,cn2an,Pinyin2Hanzi,unicodedata,inspect
PUBLIC=[name for name in base['MetaBase'].__dataclass_fields__]
DERIVED=['name','season','sea','season_seq','season_list','episode','episode_list','episodes','episode_seqs','episode_seq','season_episode','resource_term','edition','release_group','video_term','audio_term','frame_rate']
def project_full(obj):
 return dict(kind='anime' if isinstance(obj,base['MetaAnime']) else 'video',instance_keys=sorted(vars(obj)),attributes={name:getattr(obj,name) for name in PUBLIC},to_dict=obj.to_dict(),derived={name:getattr(obj,name) for name in DERIVED})

def initialized(fields):
 obj=base['MetaBase']('Synthetic',None,False)
 for key,value in fields.items():setattr(obj,key,media_type(value) if key=='type' else value)
 return obj
cases=[]
values=[None,False,True,0,1,2,-1,'','0','01','１２','١','²','-1','1_0',' 1 ',[],[1],[1,3],[3,1],['1','3'],['²'],['1','²'],[False],[None],[1,'x',3],[1,0],['١','３']]
for field in ['season','episode']:
 for i,value in enumerate(values):
  for j,fields in enumerate([{}, {'type':'电视剧','begin_'+field:2,'end_'+field:4,'total_'+field:3}]):
   cases.append(dict(name='set_'+field+'/'+str(i)+'/'+str(j),initial=fields,actions=[dict(method='set_'+field,args=[value])]))
for i,name in enumerate([None,'','中文','English','中文 Mixed',' ','１２','中文\t']):
 cases.append(dict(name='name/'+str(i),initial=dict(cn_name='旧名',en_name='Old Name'),actions=[dict(method='name',args=[name])]))
for i,pair in enumerate([(None,None),(0,0),(1,3),(3,1),(None,4),(0,3),(-1,4),(2,0)]):
 cases.append(dict(name='set_episodes/'+str(i),initial=dict(begin_episode=2,end_episode=5,total_episode=4),actions=[dict(method='set_episodes',args=list(pair))]))
queries=[None,False,True,0,1,2,4,-1,'1','+2',' 2 ','１２','1_0','x',[],[None],[True],[1],[1,2],[1,4],['1'],[2,2]]
for field in ['season','episode']:
 for i,state in enumerate([{}, {'begin_'+field:1}, {'begin_'+field:1,'end_'+field:3}, {'begin_'+field:3,'end_'+field:1}, {'end_'+field:3}]):
  cases.append(dict(name='is_in_'+field+'/'+str(i),initial=state,actions=[dict(method='is_in_'+field,args=[q]) for q in queries]))
merge_fields=['original_name','year','resource_type','resource_pix','resource_team','customization','resource_effect','video_encode','video_bit','audio_encode','fps','part','tmdbid','doubanid','media_source','media_id','episode_group','web_source','bangumiid','anilistid']
for i,empty in enumerate([None,'',0]):
 target={key:empty for key in merge_fields};target.update(type='未知',cn_name='',en_name='')
 source={key:24 if key in ['fps','tmdbid','bangumiid','anilistid'] else 'Source' for key in merge_fields};source.update(type='电视剧',cn_name='源名',en_name='Source',begin_season=2,end_season=3,total_season=2,begin_episode=4,end_episode=6,total_episode=3)
 cases.append(dict(name='merge/'+str(i),initial=target,actions=[dict(method='merge',args=[source])]))
for i,fields in enumerate([dict(type='电视剧',begin_season=0,begin_episode=0),dict(type='电影',cn_name='',en_name='Target'),dict(type='电视剧',cn_name='中文')]):
 cases.append(dict(name='merge/range/'+str(i),initial=fields,actions=[dict(method='merge',args=[dict(type='电视剧',cn_name='源',en_name='Source',begin_season=2,end_season=3,total_season=2,begin_episode=4,end_episode=6,total_episode=3)])]))
outputs=[]
for sample in cases:
 obj=initialized(sample['initial']);steps=[]
 for action in sample['actions']:
  error='';value=None
  try:
   if action['method']=='name':obj.name=action['args'][0]
   elif action['method']=='merge':obj.merge(initialized(action['args'][0]))
   else:value=getattr(obj,action['method'])(*action['args'])
  except BaseException as err:error=type(err).__name__
  steps.append(dict(action=action,value=value,error=error,state=project_full(obj)))
 outputs.append(dict(**sample,steps=steps))
bits=[]
for bit in ['8','10','12','16','9','010']:
 for sep in ['',' ','.','_','-','\u202f','\x1c']:
  for word in ['bit','BIT','bits','bıt','bİt','bitſ','bitrate']:
   for prefix,suffix in [('',''),('x',''),('','x'),('中','文')]:
    text=prefix+bit+sep+word+suffix
    bits.append(dict(text=text,result=base['MetaBase'].extract_video_bit(text)))
print(json.dumps(dict(source='actual installed complete MetaBase mutators and properties; real class, synthetic states only',source_sha256=sha,cases=outputs,video_bits=bits),ensure_ascii=False,indent=2,default=lambda value:value.value if isinstance(value,Enum) else (_ for _ in ()).throw(TypeError(type(value).__name__))))
