"""Read-only offline reference extractor: run in the original Python environment.

No application imports, config, database, credentials, or network access.
Prints the exact data consumed by the pure Go port. Runtime never uses this file.
"""
import ctypes
import json
import sys
import unicodedata as ud
from anitopy.keyword import KeywordManager

assert sys.version_info[:2] == (3, 12)
assert ud.unidata_version == '15.0.0'
scalars = [x for x in range(0x110000) if not 0xd800 <= x <= 0xdfff]
def ranges(predicate):
 out=[]
 for n in scalars:
  if predicate(chr(n)):
   if out and out[-1][1]+1==n:out[-1][1]=n
   else:out.append([n,n])
 return out

def named(character):
 try:ud.name(character);return True
 except ValueError:return False

def normalized(character):
 return ''.join(x for x in ud.normalize('NFKD',character) if not ud.combining(x)).upper()

unicode = dict(python=sys.version,unicode_version=ud.unidata_version,
 ranges=dict(digit=ranges(str.isdigit),alnum=ranges(str.isalnum),
             named=ranges(named),latin=ranges(lambda c:'LATIN' in ud.name(c,''))),
 normalized={str(n):normalized(chr(n)) for n in scalars if normalized(chr(n))!=chr(n)},
 decimal={str(n):ud.decimal(chr(n)) for n in scalars if chr(n).isdecimal()})
cased=ctypes.pythonapi._PyUnicode_IsCased
ignorable=ctypes.pythonapi._PyUnicode_IsCaseIgnorable
for fn in (cased,ignorable):fn.argtypes=[ctypes.c_uint32];fn.restype=ctypes.c_int
casing=dict(unicode_version=ud.unidata_version,
 properties=dict(cased=ranges(lambda c:bool(cased(ord(c)))),case_ignorable=ranges(lambda c:bool(ignorable(ord(c))))),
 maps={name:{str(n):getattr(chr(n),name)() for n in scalars if getattr(chr(n),name)()!=chr(n)} for name in ('lower','upper','title')})
manager=KeywordManager()
keywords={name:{key:dict(category=item.category.name.lower(),identifiable=item.options.identifiable,searchable=item.options.searchable,valid=item.options.valid) for key,item in getattr(manager,name).items()} for name in ('_keys','_file_extensions')}
print(json.dumps(dict(unicode=unicode,casing=casing,keywords=keywords),ensure_ascii=False,separators=(',',':')))
