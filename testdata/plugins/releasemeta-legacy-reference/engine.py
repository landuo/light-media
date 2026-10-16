"""Run only inside the installed container, no application modules/configs.

Input is a static synthetic corpus literal appended by offline generation.
Actual package functions are wrapped only to snapshot, not replace algorithms.
"""
import anitopy
from anitopy import parser,tokenizer
from anitopy.element import Elements
from anitopy.token import Tokens
import copy,hashlib,importlib.metadata,json,pathlib,sys,unicodedata

ROOT=pathlib.Path(anitopy.__file__).parent
SOURCE_SHA={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(ROOT.glob('*.py'))}
STAGES=[]
def snapshot(name):
 return dict(name=name,elements=copy.deepcopy(Elements.instance()._elements),check_alt_number=Elements.get_check_alt_number(),tokens=[dict(category=t.category.name,content=t.content,enclosed=t.enclosed) for t in Tokens.get_list()])
def instrument(cls,name):
 original=getattr(cls,name)
 def wrapped(self,*args,**kwargs):
  try:return original(self,*args,**kwargs)
  finally:STAGES.append(snapshot(name))
 setattr(cls,name,wrapped)
instrument(tokenizer.Tokenizer,'tokenize')
for name in ['search_for_keywords','search_for_isolated_numbers','search_for_episode_number','search_for_anime_title','search_for_release_group','search_for_episode_title','validate_elements']:instrument(parser.Parser,name)

def run(cases):
 outputs=[]
 for case in cases:
  STAGES.clear()
  options=copy.deepcopy(case.get('options',{}))
  out=dict(case)
  try:
   out['result']=anitopy.parse(case['title'],options)
   out['error']=''
  except BaseException as error:
   out['result']=None;out['error']=type(error).__name__
  out['stages']=copy.deepcopy(STAGES)
  out['final_state']=snapshot('final')
  outputs.append(out)
 print(json.dumps(dict(source='Actual installed anitopy 2.1.1; original single-title functions plus read-only snapshots; only synthetic inputs',python=sys.version,unicode_version=unicodedata.unidata_version,int_max_str_digits=sys.get_int_max_str_digits(),source_sha256=SOURCE_SHA,cases=outputs),ensure_ascii=False,separators=(',',':')))
