"""Synthetic JPEG syntax/planes only; Pillow is the reference decoder."""
from PIL import Image,features
import io,base64,json,sys,platform,struct
print(json.dumps({'kind':'metadata','pillow':Image.__version__,'jpeg_abi':features.version_codec('jpg'),'libjpeg_turbo':features.version_feature('libjpeg_turbo'),'python':sys.version,'architecture':platform.machine(),'generator':'generate_fullblock_runtime.py'}),flush=True)
def reference(name,data):
 try:
  im=Image.open(io.BytesIO(data));im.load();raw=im.tobytes();gray=im.resize((10,10)).convert('L').tobytes();average=sum(gray)/len(gray);h=''.join('1' if v>average else '0' for v in gray)
  c={'name':name,'encoded':base64.b64encode(data).decode(),'mode':im.mode,'width':im.width,'height':im.height,'pixels':base64.b64encode(raw).decode(),'gray':base64.b64encode(gray).decode(),'hash':h,'error':None}
 except Exception as e:c={'name':name,'encoded':base64.b64encode(data).decode(),'error':type(e).__name__}
 print(json.dumps(c,separators=(',',':')),flush=True)
def marker(n,payload):return bytes([255,n])+struct.pack('>H',len(payload)+2)+payload
def app14(transform):return marker(0xee,b'Adobe\x00\x64\x00\x00\x00\x00'+bytes([transform]))
def app0(jfif=True):return marker(0xe0,(b'JFIF\x00' if jfif else b'OTHER')+b'\x01\x01\x00\x00\x01\x00\x01\x00\x00')
def make_jpeg(factors,interleaved=True,ids=None,apps=b'',restart=0,w=35,h=29,extended=False,quant=1,gain=8,ac_values=()):
 n=len(factors);ids=ids or list(range(1,n+1));mh=max(x for x,y in factors);mv=max(y for x,y in factors)
 # The single-component scan convention ignores nominal factors.
 if n==1:mh=mv=1;factors=[(1,1)]
 raw=b'\xff\xd8'+apps+marker(0xdb,(b'\x00'+bytes([quant])*64) if quant<=255 else (b'\x10'+struct.pack('>H',quant)*64))
 raw+=marker(0xc1 if extended else 0xc0,bytes([8])+struct.pack('>HHB',h,w,n)+b''.join(bytes([ids[i],x*16+y,0]) for i,(x,y) in enumerate(factors)))
 raw+=marker(0xc4,b'\x00'+bytes([0,0,0,12]+[0]*12)+bytes(range(12))+b'\x10'+bytes([0]*7+[162]+[0]*8)+bytes([0,0xf0]+[run*16+cat for run in range(16) for cat in range(1,11)]))
 if restart:raw+=marker(0xdd,struct.pack('>H',restart))
 for selected in ([list(range(n))] if interleaved else [[i] for i in range(n)]):
  raw+=marker(0xda,bytes([len(selected)])+b''.join(bytes([ids[i],0]) for i in selected)+b'\x00\x3f\x00')
  bits=[];dc=[0]*n;buf=bytearray();rst=0
  def flush():
   nonlocal bits
   while len(bits)%8:bits.append(1)
   for k in range(0,len(bits),8):
    b=sum(bits[k+j]<<(7-j) for j in range(8));buf.append(b)
    if b==255:buf.append(0)
   bits=[]
  def put(v,n):bits.extend((v>>b)&1 for b in range(n-1,-1,-1))
  def block(c,bx,by):
   sample=(31*c)%224+16;coef=gain*(sample-128);delta=coef-dc[c];dc[c]=coef
   cat=abs(delta).bit_length();put(cat,4)
   if cat:put(delta if delta>=0 else delta+(1<<cat)-1,cat)
   run=0
   for value in ac_values:
    if value==0:run+=1;continue
    while run>=16:put(1,8);run-=16
    cat=abs(value).bit_length();put(2+run*10+cat-1,8);put(value if value>=0 else value+(1<<cat)-1,cat);run=0
   if run or len(ac_values)<63:put(0,8)
  if len(selected)>1:
   cols=(w+8*mh-1)//(8*mh);rows=(h+8*mv-1)//(8*mv)
  else:
   c=selected[0];hi,vi=factors[c];cols=(w*hi+8*mh-1)//(8*mh);rows=(h*vi+8*mv-1)//(8*mv)
  for by in range(rows):
   for bx in range(cols):
    for c in selected:
     hi,vi=factors[c]
     if len(selected)==1:block(c,bx,by)
     else:
      for yy in range(vi):
       for xx in range(hi):block(c,bx*hi+xx,by*vi+yy)
    num=by*cols+bx+1
    if restart and num%restart==0 and num<cols*rows:
     flush();buf.extend([255,0xd0+rst]);rst=(rst+1)%8;dc=[0]*n
  flush();raw+=bytes(buf)
 return raw+b'\xff\xd9'


patterns=[tuple([1023]*63),tuple([1023,-1023]*31+[1023]),tuple([0]*62+[1023]),tuple([0]*15+[1023]+[0]*47),tuple(((i*431+71)%2047)-1023 for i in range(63))]
for n in (1,3,4):
 for quant in (1,3,255,256,32767,65535):
  for gain in (1,16):
   for pattern,ac in enumerate(patterns):
    reference(f'fullblock-{n}c-q{quant}-g{gain}-p{pattern}',make_jpeg([(1,1)]*n,quant=quant,gain=gain,ac_values=ac,extended=quant>255,w=17,h=19))
