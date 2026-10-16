"""Synthetic JPEG table/marker references decoded by actual installed Pillow."""
from PIL import Image,features
import io,base64,json,sys,platform
print(json.dumps({'kind':'metadata','pillow':Image.__version__,'jpeg_abi':features.version_codec('jpg'),'libjpeg_turbo':features.version_feature('libjpeg_turbo'),'python':sys.version,'architecture':platform.machine(),'generator':'generate_table_runtime.py'}),flush=True)
def reference(name,data):
 try:
  im=Image.open(io.BytesIO(data));im.load();gray=im.resize((10,10)).convert('L').tobytes();avg=sum(gray)/len(gray)
  c={'name':name,'encoded':base64.b64encode(data).decode(),'mode':im.mode,'width':im.width,'height':im.height,'pixels':base64.b64encode(im.tobytes()).decode(),'gray':base64.b64encode(gray).decode(),'hash':''.join('1' if v>avg else '0' for v in gray),'error':None}
 except Exception as e:c={'name':name,'encoded':base64.b64encode(data).decode(),'error':type(e).__name__}
 print(json.dumps(c,separators=(',',':')),flush=True)
def modify(raw,fn):
 out=bytearray(raw[:2]);i=2
 while i<len(raw):
  mark=raw[i+1]
  if mark in (0xd9,):out.extend(raw[i:]);break
  length=int.from_bytes(raw[i+2:i+4],'big');data=raw[i+4:i+2+length];replacement=fn(mark,data)
  if replacement is not None:out.extend(bytes([255,mark])+(len(replacement)+2).to_bytes(2,'big')+replacement)
  i+=2+length
  if mark==0xda:out.extend(raw[i:]);break
 return bytes(out)
for mode in ('L','RGB','CMYK'):
 n={'L':1,'RGB':3,'CMYK':4}[mode];im=Image.frombytes(mode,(17,19),bytes((i*79+i//17*7)%256 for i in range(17*19*n)));b=io.BytesIO();im.save(b,'JPEG',quality=85);raw=b.getvalue()
 reference(mode+'-no-dht',modify(raw,lambda m,d:None if m==0xc4 else d))
 reference(mode+'-no-dqt',modify(raw,lambda m,d:None if m==0xdb else d))
 reference(mode+'-zero-dqt',modify(raw,lambda m,d:bytes([d[0]])+bytes(len(d)-1) if m==0xdb else d))
 for selector in (2,3):
  def select(m,d):
   if m==0xc4:d=bytes([(d[0]&0xf0)|selector])+d[1:]
   if m==0xda:d=bytes([d[0]])+b''.join(bytes([d[i],selector*17]) for i in range(1,1+d[0]*2,2))+d[1+d[0]*2:]
   return d
  # Collapse DC/chroma declarations to same selector would change table;
  # grayscale has one table only, RGB/CMYK preserve parity by using 2/3.
  def shift(m,d):
   if m==0xc4:d=bytes([d[0]+2])+d[1:]
   if m==0xda:d=bytes([d[0]])+b''.join(bytes([d[i],d[i+1]+0x22]) for i in range(1,1+d[0]*2,2))+d[1+d[0]*2:]
   return d
  reference(mode+f'-selector{selector}',modify(raw,select if mode=='L' else shift))

# Progressive quantization values are latched at each component's first
# scan. The later DQT deliberately changes every table after the first DC
# scan and must not recolor reconstructed earlier coefficients.
for mode in ('L','RGB','CMYK'):
 n={'L':1,'RGB':3,'CMYK':4}[mode];im=Image.frombytes(mode,(17,19),bytes((i*79+i//17*7)%256 for i in range(17*19*n)));b=io.BytesIO();im.save(b,'JPEG',quality=85,progressive=True);raw=b.getvalue()
 reference(mode+'-progressive-no-dht',modify(raw,lambda m,d:None if m==0xc4 else d))
 # modify() stops at first entropy scan; walk its stuffed bytes to the
 # following marker, inserting new valid 8-bit tables there.
 start=raw.find(b'\xff\xda');pos=start+2+int.from_bytes(raw[start+2:start+4],'big')
 while pos+1<len(raw):
  if raw[pos]==255 and raw[pos+1] not in (0,255):break
  pos+=1
 payload=bytes([0])+bytes([255])*64+bytes([1])+bytes([255])*64
 dqt=b'\xff\xdb'+(len(payload)+2).to_bytes(2,'big')+payload
 reference(mode+'-progressive-late-dqt',raw[:pos]+dqt+raw[pos:])
