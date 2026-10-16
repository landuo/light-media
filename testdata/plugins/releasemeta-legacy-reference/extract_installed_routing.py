"""Read-only reference for MetaInfo's actual third-party regex Unicode tables."""
import regex,json,pathlib,hashlib
text=''.join(chr(n) for n in range(0x110000) if not 0xd800<=n<=0xdfff)
def ranges(pattern):
 out=[]
 for m in regex.finditer(pattern,text):
  n=ord(m.group())
  if out and out[-1][1]+1==n:out[-1][1]=n
  else:out.append([n,n])
 return out
print(json.dumps(dict(version=regex.__version__,unicode_digit=ranges(r'\d'),unicode_space=ranges(r'\s'),extension_sha256={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in pathlib.Path(regex.__file__).parent.glob('*.so')},casefold_letters={c:[ord(m.group()) for m in regex.finditer(c,text,regex.IGNORECASE)] for c in 'XVPISEv'}),ensure_ascii=False,indent=2))
