"""Synthetic in-memory JPEG references only; no site/user image inputs."""
from PIL import Image,features
import io,base64,json,sys,platform,hashlib
meta={'kind':'metadata','pillow':Image.__version__,'jpeg_abi':features.version_codec('jpg'),'libjpeg_turbo':features.version_feature('libjpeg_turbo'),'python':sys.version,'architecture':platform.machine(),'generator':'generate_runtime.py'}
print(json.dumps(meta),flush=True)
def reference(name,data):
 try:
  im=Image.open(io.BytesIO(data));im.load();raw=im.tobytes();gray=im.resize((10,10)).convert('L').tobytes();average=sum(gray)/len(gray);h=''.join('1' if v>average else '0' for v in gray)
  c={'name':name,'encoded':base64.b64encode(data).decode(),'mode':im.mode,'width':im.width,'height':im.height,'pixels':base64.b64encode(raw).decode(),'gray':base64.b64encode(gray).decode(),'hash':h,'error':None}
 except Exception as e:c={'name':name,'encoded':base64.b64encode(data).decode(),'error':type(e).__name__}
 print(json.dumps(c,separators=(',',':')),flush=True)
def frame(mode,w,h,pattern):
 channels={'L':1,'RGB':3,'CMYK':4}[mode];pix=bytearray()
 for y in range(h):
  for x in range(w):
   for c in range(channels):
    if pattern=='checker':v=255 if (x+y+c)%2 else 0
    elif pattern=='edge':v=255 if x>w//2 else 0 if y>h//2 else (c*79)%256
    elif pattern=='solid':v=(17+c*109)%256
    else:v=(x*37+y*71+c*103+x*y*13+(x^y)*19)%256
    pix.append(v)
 return Image.frombytes(mode,(w,h),bytes(pix))
def jpeg(im,quality,subsampling,progressive,**kw):
 b=io.BytesIO();im.save(b,'JPEG',quality=quality,subsampling=subsampling,progressive=progressive,**kw);return b.getvalue()
sizes=[(1,1),(1,2),(2,1),(2,3),(3,2),(3,3),(4,5),(5,4),(7,9),(8,8),(9,7),(15,17),(16,16),(17,15),(31,33),(32,32),(33,31),(127,95),(3,211),(211,3)]
for mode in ['L','RGB','CMYK']:
 for w,h in sizes:
  im=frame(mode,w,h,'noise')
  for sub in ([0,1,2] if mode=='RGB' else [0]):
   for progressive in (False,True):
    for q in (1,50,95,100):reference(f'{mode}-{w}x{h}-q{q}-s{sub}-p{int(progressive)}',jpeg(im,q,sub,progressive))
for pattern in ('checker','edge','solid'):
 for mode in ('L','RGB','CMYK'):
  for sub in ([0,1,2] if mode=='RGB' else [0]):
   for progressive in (False,True):
    im=frame(mode,19,23,pattern)
    for q in (1,99):reference(f'{pattern}-{mode}-q{q}-s{sub}-p{int(progressive)}',jpeg(im,q,sub,progressive))
# Restart intervals and metadata variants exercise marker/entropy boundaries.
for progressive in (False,True):
 im=frame('RGB',73,59,'noise')
 for option in ('restart_marker_blocks','restart_marker_rows'):
  for count in (1,2,7):reference(f'{option}-{count}-p{int(progressive)}',jpeg(im,83,2,progressive,**{option:count}))
# JPEGs produced in memory are modified only in APP markers/headers.
def remove_markers(raw,kinds):
 out=bytearray(raw[:2]);i=2
 while i<len(raw):
  if raw[i]!=255:out.extend(raw[i:]);break
  marker=raw[i+1]
  if marker in (0xd9,0xda):out.extend(raw[i:]);break
  n=int.from_bytes(raw[i+2:i+4],'big');seg=raw[i:i+2+n]
  if marker not in kinds:out.extend(seg)
  i+=2+n
 return bytes(out)
def adobe(raw,transform):
 payload=b'Adobe\x00\x64\x00\x00\x00\x00'+bytes([transform]);return raw[:2]+b'\xff\xee'+(len(payload)+2).to_bytes(2,'big')+payload+raw[2:]
for mode in ('RGB','CMYK'):
 im=frame(mode,17,19,'noise');raw=jpeg(im,90,0,False);raw=remove_markers(raw,{0xe0,0xee})
 reference(mode+'-no-app',raw)
 for transform in (0,1,2):reference(mode+'-adobe-'+str(transform),adobe(raw,transform))
# Extreme valid quantization tables, chroma inputs and optimize entropy output.
for q in (1,2,255):
 im=frame('RGB',23,25,'checker');b=io.BytesIO();im.save(b,'JPEG',qtables=[[q]*64,[q]*64],subsampling=2,optimize=True);reference('qtables-'+str(q),b.getvalue())
# Deterministic malformed inputs: acceptance/error, never fuzz external files.
raw=jpeg(frame('RGB',17,19,'noise'),83,2,False)
for n in (0,1,2,10,50,len(raw)//2,len(raw)-2,len(raw)-1):reference('truncated-'+str(n),raw[:n])
reference('trailing-data',raw+b'synthetic trailing bytes')
