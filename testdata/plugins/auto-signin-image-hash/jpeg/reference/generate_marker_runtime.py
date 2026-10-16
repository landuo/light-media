"""Synthetic JPEG syntax/planes only; Pillow is the reference decoder."""
from PIL import Image,features
import io,base64,json,sys,platform,struct
print(json.dumps({'kind':'metadata','pillow':Image.__version__,'jpeg_abi':features.version_codec('jpg'),'libjpeg_turbo':features.version_feature('libjpeg_turbo'),'python':sys.version,'architecture':platform.machine(),'generator':'generate_marker_runtime.py'}),flush=True)
def reference(name,data):
 try:
  im=Image.open(io.BytesIO(data));im.load();raw=im.tobytes();gray=im.resize((10,10)).convert('L').tobytes();average=sum(gray)/len(gray);h=''.join('1' if v>average else '0' for v in gray)
  c={'name':name,'encoded':base64.b64encode(data).decode(),'mode':im.mode,'width':im.width,'height':im.height,'pixels':base64.b64encode(raw).decode(),'gray':base64.b64encode(gray).decode(),'hash':h,'error':None}
 except Exception as e:c={'name':name,'encoded':base64.b64encode(data).decode(),'error':type(e).__name__}
 print(json.dumps(c,separators=(',',':')),flush=True)
def marker(n,payload):return bytes([255,n])+struct.pack('>H',len(payload)+2)+payload
def app14(transform):return marker(0xee,b'Adobe\x00\x64\x00\x00\x00\x00'+bytes([transform]))
def app0(jfif=True):return marker(0xe0,(b'JFIF\x00' if jfif else b'OTHER')+b'\x01\x01\x00\x00\x01\x00\x01\x00\x00')
def make_jpeg(factors,interleaved=True,ids=None,apps=b'',restart=0,w=35,h=29,extended=False):
 n=len(factors);ids=ids or list(range(1,n+1));mh=max(x for x,y in factors);mv=max(y for x,y in factors)
 # The single-component scan convention ignores nominal factors.
 if n==1:mh=mv=1;factors=[(1,1)]
 raw=b'\xff\xd8'+apps+marker(0xdb,b'\x00'+bytes([1])*64)
 raw+=marker(0xc1 if extended else 0xc0,bytes([8])+struct.pack('>HHB',h,w,n)+b''.join(bytes([ids[i],x*16+y,0]) for i,(x,y) in enumerate(factors)))
 raw+=marker(0xc4,b'\x00'+bytes([0,0,0,12]+[0]*12)+bytes(range(12))+b'\x10'+bytes([1]+[0]*15)+b'\x00')
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
   sample=(31*c+37*bx+73*by)%224+16;coef=8*(sample-128);delta=coef-dc[c];dc[c]=coef
   cat=abs(delta).bit_length();put(cat,4)
   if cat:put(delta if delta>=0 else delta+(1<<cat)-1,cat)
   put(0,1)
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
# Integral and fractional sampling, first component need not have maxima.
sets=[[(1,1)]*3,[(1,2),(1,1),(1,1)],[(2,1),(1,1),(1,1)],[(2,2),(1,1),(1,1)],[(4,1),(1,1),(1,1)],[(4,2),(1,1),(1,1)],[(3,1),(1,1),(1,1)],[(1,3),(1,1),(1,1)],[(1,4),(1,1),(1,1)],[(4,4),(1,1),(1,1)],[(1,1),(2,2),(1,1)],[(2,1),(2,1),(1,1)],[(4,2),(2,1),(1,1)],[(3,1),(2,1),(1,1)],[(1,3),(1,2),(1,1)]]
for j,f in enumerate(sets):
 for interleaved in (False,True):
  for restart in (0,1,7):reference(f'sampling-{j}-i{int(interleaved)}-r{restart}',make_jpeg(f,interleaved,restart=restart))
for n in (1,4):
 factors=[[(1,1)]*n]
 if n==4:factors += [[(2,2),(1,1),(1,1),(2,2)],[(1,2)]*4,[(1,1),(2,1),(1,1),(1,1)],[(4,4)]*4]
 for j,f in enumerate(factors):
  for interleaved in (False,True):
   for transform in (None,0,1,2,255):
    reference(f'{n}components-{j}-i{int(interleaved)}-adobe{transform}',make_jpeg(f,interleaved,apps=b'' if transform is None else app14(transform),restart=1))
# Colorspace selection must respect Adobe/JFIF priority over ASCII IDs;
# a later unrelated APP0 must not clear the earlier saw_JFIF flag.
for ids in ([1,2,3],list(b'RGB'),[7,8,9]):
 for transform in (None,0,1,2,255):
  for jfif in (False,True):
   a=(app0() if jfif else b'')+(app14(transform) if transform is not None else b'')
   reference(f'colors-{ids}-a{transform}-jfif{int(jfif)}',make_jpeg([(1,1)]*3,ids=ids,apps=a))
reference('jfif-sticky',make_jpeg([(1,1)]*3,ids=list(b'RGB'),apps=app0()+app0(False)))
reference('extended-sof1',make_jpeg([(2,2),(1,1),(1,1)],extended=True))
# Non-entropy marker corruption/trailing boundaries; kept in own corpus.
raw=make_jpeg([(2,2),(1,1),(1,1)])
for name,data in [('no-soi',raw[2:]),('empty-eoi',b'\xff\xd8\xff\xd9'),('sof-only',raw[:raw.find(b'\xff\xda')]+b'\xff\xd9'),('duplicated-soi',raw[:2]+raw),('fill-marker',raw[:2]+b'\xff\xff'+raw[3:]),('extraneous-before-dqt',raw[:2]+b'synthetic'+raw[2:])]:reference(name,data)
