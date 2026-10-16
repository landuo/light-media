"""Additional synthetic PNG/GIF and resampling boundaries; no real images."""
import ast
import base64
import gzip
import hashlib
import io
import json
import pathlib
import struct
import sys

from PIL import Image, PngImagePlugin

source=pathlib.Path('/app/app/plugins/autosignin/sites/tjupt.py').read_bytes()
tree=ast.parse(source)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='Tjupt')
method=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='_tohash')
namespace={}
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'installed-tjupt','exec'),namespace)
tohash=namespace['_tohash']
def b64(b): return base64.b64encode(b).decode()

encoded=[]
def add(name,data):
    im=Image.open(io.BytesIO(data))
    mode=im.mode
    gray=im.resize((10,10)).convert('L').tobytes()
    encoded.append(dict(name=name,encoded=b64(data),mode=mode,width=im.width,height=im.height,gray=b64(gray),hash=tohash(im)))

for mode in ['L','RGB','P','RGBA','LA','1']:
    w,h=17,13
    n=len(Image.new(mode,(1,1)).getbands())
    if mode=='1':
        im=Image.new(mode,(w,h));im.putdata([255 if (x*19)%31>15 else 0 for x in range(w*h)])
    else:
        data=bytes((i*37+i//n*71)%256 for i in range(w*h*n))
        im=Image.frombytes(mode,(w,h),data)
    if mode=='P': im.putpalette(bytes((i*73+i//3*17)%256 for i in range(768)))
    for bits in ([1,2,4,8] if mode=='P' else [8]):
        buf=io.BytesIO();im.save(buf,'PNG',bits=bits);add(f'PNG-{mode}-bits{bits}',buf.getvalue())
    if mode in ('L','RGB','P'):
        tr=(0,37,74) if mode=='RGB' else (bytes((255-i)%256 for i in range(256)) if mode=='P' else 0)
        buf=io.BytesIO();im.save(buf,'PNG',transparency=tr);add(f'PNG-{mode}-transparency',buf.getvalue())
    if mode in ('L','P'):
        buf=io.BytesIO();im.save(buf,'GIF',transparency=3,comment=b'controlled comment; image offsets next')
        data=buf.getvalue()
        add(f'GIF-{mode}-transparent',data)
        # Rewrite only logical screen and first image offset: same first frame
        # decoder data, but a larger initialized canvas surrounds the image.
        b=bytearray(data);b[6:10]=struct.pack('<HH',w+7,h+5)
        i=13+(3*(2<<(b[10]&7)) if b[10]&128 else 0)
        while b[i]==33:
            i+=2
            while b[i]: i+=b[i]+1
            i+=1
        assert b[i]==44
        b[i+1:i+5]=struct.pack('<HH',4,2)
        add(f'GIF-{mode}-offset-transparent',bytes(b))
        # First local palette overrides the global one without losing the
        # original RGB of the transparent palette entry.
        palette=bytes((j*19+j//3*83)%256 for j in range(768))
        b=bytearray(data)
        b[i+9]=b[i+9]|128|7
        b[i+10:i+10]=palette
        add(f'GIF-{mode}-local-palette',bytes(b))

# Explicit packed 2/4-bit grayscale PNGs built with Pillow's PNG chunk writer.
for depth in (2,4):
    w,h=19,13
    rows=[]
    mask=(1<<depth)-1
    for y in range(h):
        row=bytearray((w*depth+7)//8)
        for x in range(w): row[(x*depth)//8]|=((x*3+y*5)&mask)<<(8-depth-(x*depth)%8)
        rows.append(b'\0'+row)
    import zlib
    out=io.BytesIO();out.write(b'\x89PNG\r\n\x1a\n')
    PngImagePlugin.putchunk(out,b'IHDR',struct.pack('>IIBBBBB',w,h,depth,0,0,0,0))
    PngImagePlugin.putchunk(out,b'IDAT',zlib.compress(b''.join(rows)))
    PngImagePlugin.putchunk(out,b'IEND',b'')
    add(f'PNG-L-bit{depth}',out.getvalue())

raw=[]
pal=bytes((i*71+i//3*11)%256 for i in range(768))
for mode in ['1','P','L','LA','RGB','RGBA','CMYK']:
    channels=len(Image.new(mode,(1,1)).getbands())
    for w,h in [(3,6),(5,11),(11,5),(10,10),(2,201),(1,101)]:
        px=bytes((i*37+i//channels*41)%256 for i in range(w*h*channels))
        if mode=='1':
            px=bytes(255 if v>127 else 0 for v in px);im=Image.new(mode,(w,h));im.putdata(px)
        else: im=Image.frombytes(mode,(w,h),px)
        if mode=='P': im.putpalette(pal)
        for shape in [(w,h),(w*2,h*2),(w,3),(3,h),(14,17),(1,1)]:
            raw.append(dict(name=f'{mode}-{w}x{h}-to{shape[0]}x{shape[1]}',Mode=mode,Width=w,Height=h,Pixels=b64(px),Palette=b64(pal) if mode=='P' else '',shape=shape,gray=b64(im.resize(shape).convert('L').tobytes()),hash=tohash(im,shape)))

result=dict(source_sha256=hashlib.sha256(source).hexdigest(),raw=raw,encoded=encoded)
sys.stdout.buffer.write(gzip.compress(json.dumps(result,separators=(',',':')).encode(),mtime=0))
