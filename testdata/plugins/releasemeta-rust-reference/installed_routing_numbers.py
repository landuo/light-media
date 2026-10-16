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

REGEX_SOURCE=json.loads('{\n  "crate": "regex-syntax",\n  "version": "0.8.10",\n  "crate_sha256": "dc897dd8d9e8bd1ed8cdad82b5966c3e0ecae09fb1907d58efaa013543185d0a",\n  "unicode_version": "16.0.0",\n  "source_sha256": {\n    "perl_decimal.rs": "6a59143db81a0bcaf0e8d0af265e711d1a6472e1f091ee9ee4377da5d5d0cd1f",\n    "perl_space.rs": "ec9bb22ed7e99feef292249c7e6f4673ee0af9635d4d158f93923494c14cd5ed",\n    "case_folding_simple.rs": "7622c7f7f03ac0dc2f2bcd51c81a217d64de0cc912f62f1add5f676603a02456"\n  },\n  "decimal_ranges": [\n    [\n      48,\n      57\n    ],\n    [\n      1632,\n      1641\n    ],\n    [\n      1776,\n      1785\n    ],\n    [\n      1984,\n      1993\n    ],\n    [\n      2406,\n      2415\n    ],\n    [\n      2534,\n      2543\n    ],\n    [\n      2662,\n      2671\n    ],\n    [\n      2790,\n      2799\n    ],\n    [\n      2918,\n      2927\n    ],\n    [\n      3046,\n      3055\n    ],\n    [\n      3174,\n      3183\n    ],\n    [\n      3302,\n      3311\n    ],\n    [\n      3430,\n      3439\n    ],\n    [\n      3558,\n      3567\n    ],\n    [\n      3664,\n      3673\n    ],\n    [\n      3792,\n      3801\n    ],\n    [\n      3872,\n      3881\n    ],\n    [\n      4160,\n      4169\n    ],\n    [\n      4240,\n      4249\n    ],\n    [\n      6112,\n      6121\n    ],\n    [\n      6160,\n      6169\n    ],\n    [\n      6470,\n      6479\n    ],\n    [\n      6608,\n      6617\n    ],\n    [\n      6784,\n      6793\n    ],\n    [\n      6800,\n      6809\n    ],\n    [\n      6992,\n      7001\n    ],\n    [\n      7088,\n      7097\n    ],\n    [\n      7232,\n      7241\n    ],\n    [\n      7248,\n      7257\n    ],\n    [\n      42528,\n      42537\n    ],\n    [\n      43216,\n      43225\n    ],\n    [\n      43264,\n      43273\n    ],\n    [\n      43472,\n      43481\n    ],\n    [\n      43504,\n      43513\n    ],\n    [\n      43600,\n      43609\n    ],\n    [\n      44016,\n      44025\n    ],\n    [\n      65296,\n      65305\n    ],\n    [\n      66720,\n      66729\n    ],\n    [\n      68912,\n      68921\n    ],\n    [\n      68928,\n      68937\n    ],\n    [\n      69734,\n      69743\n    ],\n    [\n      69872,\n      69881\n    ],\n    [\n      69942,\n      69951\n    ],\n    [\n      70096,\n      70105\n    ],\n    [\n      70384,\n      70393\n    ],\n    [\n      70736,\n      70745\n    ],\n    [\n      70864,\n      70873\n    ],\n    [\n      71248,\n      71257\n    ],\n    [\n      71360,\n      71369\n    ],\n    [\n      71376,\n      71395\n    ],\n    [\n      71472,\n      71481\n    ],\n    [\n      71904,\n      71913\n    ],\n    [\n      72016,\n      72025\n    ],\n    [\n      72688,\n      72697\n    ],\n    [\n      72784,\n      72793\n    ],\n    [\n      73040,\n      73049\n    ],\n    [\n      73120,\n      73129\n    ],\n    [\n      73552,\n      73561\n    ],\n    [\n      90416,\n      90425\n    ],\n    [\n      92768,\n      92777\n    ],\n    [\n      92864,\n      92873\n    ],\n    [\n      93008,\n      93017\n    ],\n    [\n      93552,\n      93561\n    ],\n    [\n      118000,\n      118009\n    ],\n    [\n      120782,\n      120831\n    ],\n    [\n      123200,\n      123209\n    ],\n    [\n      123632,\n      123641\n    ],\n    [\n      124144,\n      124153\n    ],\n    [\n      124401,\n      124410\n    ],\n    [\n      125264,\n      125273\n    ],\n    [\n      130032,\n      130041\n    ]\n  ],\n  "space_ranges": [\n    [\n      9,\n      13\n    ],\n    [\n      32,\n      32\n    ],\n    [\n      133,\n      133\n    ],\n    [\n      160,\n      160\n    ],\n    [\n      5760,\n      5760\n    ],\n    [\n      8192,\n      8202\n    ],\n    [\n      8232,\n      8233\n    ],\n    [\n      8239,\n      8239\n    ],\n    [\n      8287,\n      8287\n    ],\n    [\n      12288,\n      12288\n    ]\n  ]\n}')
CASES=[]
for n in ['一一','十十','二三','十二','一百一一','零一','1一','一1','一十','十百','百十','100']:
 CASES.append(dict(name='rust-number/subtitle/'+n,title='[Group] Synthetic Show - 01 [1080p]',subtitle='第'+n+'季',options={}))
 CASES.append(dict(name='rust-number/title/'+n,title='[Group] 中文第'+n+'季 - 01 [1080p]',subtitle=None,options={}))
for n in ['１２','١','𐵁','𞗱']:
 for i,title in enumerate(['[Group][中文'+n+'][01][1080p]','[Group] Example'+n+'Show - '+n+' [1080p]','[Group][TV '+n+'][中文标题][1080p]']):
  CASES.append(dict(name='rust-regex/wrapper/'+str(ord(n[0]))+'/'+str(i),title=title,subtitle=None,options={}))
ROUTES=[]
points=sorted({p for a,b in REGEX_SOURCE["decimal_ranges"] for p in [a-1,a,b,b+1]})
for p in points:
 c=chr(p)
 for i,title in enumerate(['[Group] Show - '+c+' [1080p]','[Group] Show - '+c*4+' [1080p]','[Group] Show - '+c*5+' [1080p]','[Group] Show S'+c*2+' [01][1080p]','[01][Show S'+c+' [1080p]']):ROUTES.append(dict(title=title))
for p in sorted({p for a,b in REGEX_SOURCE["space_ranges"] for p in [a-1,a,b,b+1]}):
 c=chr(p)
 for title in ['[Group] Show'+c+'-'+c+'01'+c+'[1080p]','[01]'+c+'[Show]']:
  ROUTES.append(dict(title=title))
for c in 'IiıİSsſXVPIxvpi':
 for title in ['['+c+'][Show]','【'+c+'】 【Show】','[01][Show '+c+'01 [1080p]']:
  ROUTES.append(dict(title=title))

settings.RUST_ACCEL=True
runtime.supports_extended_media_ids.cache_clear()
wrapper_cases=[]
for sample in CASES:
 options=sample['options'];values.clear();values.update(words=options.get('custom_words',[]),release_groups=options.get('release_groups',[]),customization=options.get('customization',[]))
 meta._rust_parse_options=lambda words=None:{'custom_words':options.get('custom_words',[]) if words is None else words,'media_exts':settings.RMT_MEDIAEXT+settings.RMT_SUBEXT+settings.RMT_AUDIOEXT,'release_groups':base['ReleaseGroupsMatcher']().get_release_groups(),'customization':base['CustomizationMatcher'].normalize_customization(options.get('customization',[])),'streaming_platforms':base['StreamingPlatforms']().get_lookup_cache()}
 out=dict(sample)
 try:
  obj=meta.MetaInfo(sample['title'],sample['subtitle'],None)
  out['rust']=dict(error='',result=project(obj,'anime' if isinstance(obj,base['MetaAnime']) else 'video'))
 except (KeyboardInterrupt,SystemExit):raise
 except BaseException as e:out['rust']=dict(error=type(e).__name__,result=None)
 wrapper_cases.append(out)
route_options={'custom_words':[],'media_exts':[],'release_groups':'','customization':[],'streaming_platforms':{}}
for sample in ROUTES:
 parsed=moviepilot_rust.parse_metainfo_fast(sample['title'],None,route_options)
 assert parsed is not None, sample
 sample['is_anime']=parsed.get('kind')=='anime'
print(json.dumps(dict(source='actual installed wheel and isolated actual MetaInfo/rust_accel; synthetic cases only',source_sha256=sha,distributions={n:importlib.metadata.version(n) for n in ['moviepilot-rust','regex','anitopy']},regex_source=REGEX_SOURCE,cases=wrapper_cases,routes=ROUTES),ensure_ascii=False,indent=2))
