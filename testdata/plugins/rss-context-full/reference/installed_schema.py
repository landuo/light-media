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

contextmod=load('app/core/context.py',dict(base,MetaInfo=meta.MetaInfo))
identitymod=load('app/utils/media.py')
objects={name:cls() for name,cls in [('MediaInfo',contextmod.MediaInfo),('TorrentInfo',contextmod.TorrentInfo)]}
properties={name:[key for key in dir(cls) if isinstance(getattr(cls,key),property)] for name,cls in [('MediaInfo',contextmod.MediaInfo),('TorrentInfo',contextmod.TorrentInfo)]}
print(json.dumps(dict(source_sha256=sha,defaults={name:vars(obj)for name,obj in objects.items()},properties=properties,casefold={str(n):chr(n).casefold()for n in range(0x110000)if not 0xd800<=n<=0xdfff and chr(n).casefold()!=chr(n)},unicode=unicodedata.unidata_version),ensure_ascii=False,indent=2,default=lambda x:x.value if isinstance(x,Enum)else (_ for _ in ()).throw(TypeError(type(x).__name__))))
