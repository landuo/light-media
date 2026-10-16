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
# These two path helpers are unreachable: this reference exercises MetaInfo(title).
def unused(*args, **kwargs):
 raise AssertionError('unexpected path parser invocation')
base.update(clear_parsed_title_for_parent_merge=unused, should_use_parent_title_for_file_stem=unused,
            rust_accel=NS(parse_metainfo=lambda *args:None, find_metainfo=lambda *args:None, supports_extended_media_ids=lambda:False))
meta = load('app/core/metainfo.py', base)
# Avoid default runtime config collection in the intentionally disabled Rust path.
meta._rust_parse_options = lambda words=None: {}
import moviepilot_rust
assert importlib.metadata.version('moviepilot-rust') == '0.2.6'
assert moviepilot_rust.is_available()
FIELDS = ['resource_pix','resource_type','resource_effect','resource_team','customization','begin_season','end_season']
def project(obj, kind):
 result = {k:getattr(obj,k) for k in FIELDS}
 result.update(season=obj.season, kind=kind, name=obj.name, type=obj.type.value, begin_episode=obj.begin_episode, end_episode=obj.end_episode)
 return result
cases = []
def case(name, title, subtitle=None, options=None):
 options = dict(options or {})
 values.clear()
 values.update(words=options.get('custom_words',[]), customization=options.get('customization',[]), release_groups=options.get('release_groups',[]))
 legacy = meta.MetaInfo(title, subtitle, options.get('custom_words'))
 rust_options = {
  'custom_words':options.get('custom_words',[]),
  'media_exts':settings.RMT_MEDIAEXT + settings.RMT_SUBEXT + settings.RMT_AUDIOEXT,
  'release_groups':base['ReleaseGroupsMatcher']().get_release_groups(),
  'customization':base['CustomizationMatcher'].normalize_customization(options.get('customization',[])),
  'streaming_platforms':base['StreamingPlatforms']().get_lookup_cache(),
 }
 parsed = moviepilot_rust.parse_metainfo_fast(title, subtitle, rust_options)
 rust = meta._meta_from_rust(parsed)
 assert rust is not None, (name, title, parsed)
 cases.append(dict(name=name,title=title,subtitle=subtitle,options=options,
                   legacy=project(legacy,'anime' if isinstance(legacy,base['MetaAnime']) else 'video'),
                   rust=project(rust,parsed.get('kind'))))

# This supplemental reference invokes the actual installed MetaInfo wrapper,
# including rust_accel BaseException fallback; no application modules are imported.
runtime = load('app/utils/rust_accel.py', dict(settings=settings,logger=logger,log_settings=NS(DEBUG=False,LOG_LEVEL='INFO')))
meta.rust_accel = runtime
wrapper_cases=[]
def wrapper_case(name,title,subtitle=None,options=None):
 options=dict(options or {})
 values.clear();values.update(words=options.get('custom_words',[]),customization=options.get('customization',[]),release_groups=options.get('release_groups',[]))
 meta._rust_parse_options=lambda custom_words=None:{
  'custom_words':options.get('custom_words',[]) if custom_words is None else custom_words,
  'media_exts':settings.RMT_MEDIAEXT+settings.RMT_SUBEXT+settings.RMT_AUDIOEXT,
  'release_groups':base['ReleaseGroupsMatcher']().get_release_groups(),
  'customization':base['CustomizationMatcher'].normalize_customization(options.get('customization',[])),
  'streaming_platforms':base['StreamingPlatforms']().get_lookup_cache(),
 }
 out=dict(name=name,title=title,subtitle=subtitle,options=options)
 for mode in ['legacy']:
  settings.RUST_ACCEL=mode=='rust'
  runtime.supports_extended_media_ids.cache_clear()
  try:
   value=meta.MetaInfo(title,subtitle,options.get('custom_words'))
   out[mode]=dict(error='',result=project(value,'anime' if isinstance(value,base['MetaAnime']) else 'video'))
  except (KeyboardInterrupt,SystemExit):
   raise
  except BaseException as error:
   out[mode]=dict(error=type(error).__name__,result=None)
 wrapper_cases.append(out)

import multiprocessing as mp
def evaluate(sample,send):
 wrapper_cases.clear()
 try:
  wrapper_case(sample['name'],sample['title'],sample.get('subtitle'),sample.get('options'))
  value=wrapper_cases[-1]
  value['options']['media_exts']=settings.RMT_MEDIAEXT+settings.RMT_SUBEXT+settings.RMT_AUDIOEXT
  send.send(value)
 except BaseException as error:send.send(dict(name=sample['name'],title=sample['title'],setup_error=type(error).__name__))
outputs=[]
for sample in [{'name': 'python15-wrapper/0/0', 'title': '[Group][中文²][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/0/1', 'title': '[Group] Example²Show - ² [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/0/2', 'title': '[Group][TV ²][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/0/3', 'title': '[Group] Example Show S²E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/1/0', 'title': '[Group][中文١][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/1/1', 'title': '[Group] Example١Show - ١ [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/1/2', 'title': '[Group][TV ١][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/1/3', 'title': '[Group] Example Show S١E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/2/0', 'title': '[Group][中文１２][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/2/1', 'title': '[Group] Example１２Show - １２ [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/2/2', 'title': '[Group][TV １２][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/2/3', 'title': '[Group] Example Show S１２E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/3/0', 'title': '[Group][中文𑽑][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/3/1', 'title': '[Group] Example𑽑Show - 𑽑 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/3/2', 'title': '[Group][TV 𑽑][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/3/3', 'title': '[Group] Example Show S𑽑E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/4/0', 'title': '[Group][中文𐵁][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/4/1', 'title': '[Group] Example𐵁Show - 𐵁 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/4/2', 'title': '[Group][TV 𐵁][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/4/3', 'title': '[Group] Example Show S𐵁E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/5/0', 'title': '[Group][中文𞗱][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/5/1', 'title': '[Group] Example𞗱Show - 𞗱 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/5/2', 'title': '[Group][TV 𞗱][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/5/3', 'title': '[Group] Example Show S𞗱E01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/6/0', 'title': '[Group][中文\x1c][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/6/1', 'title': '[Group] Example\x1cShow - \x1c [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/6/2', 'title': '[Group][TV \x1c][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/6/3', 'title': '[Group] Example Show S\x1cE01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/7/0', 'title': '[Group][中文\u202f][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/7/1', 'title': '[Group] Example\u202fShow - \u202f [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/7/2', 'title': '[Group][TV \u202f][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/7/3', 'title': '[Group] Example Show S\u202fE01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/8/0', 'title': '[Group][中文ͅ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/8/1', 'title': '[Group] ExampleͅShow - ͅ [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/8/2', 'title': '[Group][TV ͅ][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/8/3', 'title': '[Group] Example Show SͅE01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/9/0', 'title': '[Group][中文Σ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/9/1', 'title': '[Group] ExampleΣShow - Σ [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/9/2', 'title': '[Group][TV Σ][中文标题][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-wrapper/9/3', 'title': '[Group] Example Show SΣE01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/0/0', 'title': '[Group] 123abcΟΣ - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/0/1', 'title': '[Group][123abcΟΣ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/1/0', 'title': '[Group] lǰteststraße - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/1/1', 'title': '[Group][lǰteststraße][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/2/0', 'title': '[Group] ⅱFooΟΣ - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/2/1', 'title': '[Group][ⅱFooΟΣ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/3/0', 'title': '[Group] aΣaͅ - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/3/1', 'title': '[Group][aΣaͅ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/4/0', 'title': '[Group] aΣͅ - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/4/1', 'title': '[Group][aΣͅ][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/5/0', 'title': '[Group] ßßExample - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/5/1', 'title': '[Group][ßßExample][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/6/0', 'title': '[Group] ǳExample - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/6/1', 'title': '[Group][ǳExample][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/7/0', 'title': '[Group] 𐗀Example - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/7/1', 'title': '[Group][𐗀Example][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/8/0', 'title': '[Group] \U00016ea0Example - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/8/1', 'title': '[Group][\U00016ea0Example][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/9/0', 'title': '[Group] \U00011db0Example - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/9/1', 'title': '[Group][\U00011db0Example][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/10/0', 'title': '[Group] 中文第十十季 - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/10/1', 'title': '[Group][中文第十十季][01][1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/11/0', 'title': '[Group] 中文第一一季 - 01 [1080p]', 'subtitle': None, 'options': {}}, {'name': 'python15-name/11/1', 'title': '[Group][中文第一一季][01][1080p]', 'subtitle': None, 'options': {}}]:
 read,send=mp.Pipe(duplex=False)
 worker=mp.Process(target=evaluate,args=(sample,send));worker.start();send.close();worker.join(2)
 if worker.is_alive():
  worker.terminate();worker.join();outputs.append(dict(**sample,reference_status='non_terminating_within_2_seconds'))
 else:
  result=read.recv() if read.poll() else dict(**sample,reference_status='no_result')
  if result.get('legacy',{}).get('result',{}).get('kind')=='anime' or 'reference_status' in result:outputs.append(result)
 read.close()
print(json.dumps(dict(source='actual installed MetaInfo complete legacy wrapper Unicode15 cases',source_sha256=sha,cases=outputs),ensure_ascii=False,indent=2))
