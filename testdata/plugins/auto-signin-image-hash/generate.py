"""Offline only: run selected installed AST methods on synthetic Pillow rasters.
No app imports, network, user files or writes. Emits compressed JSON to stdout.
"""
import ast
import base64
import gzip
import hashlib
import io
import json
import pathlib
import sys

import PIL
from PIL import Image, features

path = pathlib.Path('/app/app/plugins/autosignin/sites/tjupt.py')
source = path.read_bytes()
tree = ast.parse(source)
cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'Tjupt')
methods = [n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name in ('_tohash', '_comparehash')]
namespace = {}
exec(compile(ast.fix_missing_locations(ast.Module(body=methods, type_ignores=[])), str(path), 'exec'), namespace)
tohash, compare = namespace['_tohash'], namespace['_comparehash']
palette = bytes((i * 43 + (i // 3) * 17) % 256 for i in range(768))

def b64(raw):
    return base64.b64encode(raw).decode('ascii')

def make(mode, w, h, pattern):
    channels = {'1': 1, 'P': 1, 'L': 1, 'LA': 2, 'RGB': 3, 'RGBA': 4, 'RGBX': 4, 'CMYK': 4}[mode]
    raw = bytearray()
    state = 0x120091 + w * 71 + h * 41 + pattern * 119
    for y in range(h):
        for x in range(w):
            for c in range(channels):
                state = (state * 1664525 + 1013904223) & 0xffffffff
                v = (state >> 24) if pattern == 0 else ((127 if c < channels - 1 else 0) if pattern == 1 else ((x * 37 + y * 19 + c * 83) % 256))
                if mode == '1':
                    v = 255 if v > 127 else 0
                raw.append(v)
    if mode == '1':
        im = Image.new('1', (w, h))
        im.putdata(raw)
    else:
        im = Image.frombytes(mode, (w, h), bytes(raw))
    if mode == 'P':
        im.putpalette(palette)
    return im, bytes(raw)

raw_cases = []
encoded_cases = []
dims = [(1,1), (1,7), (7,1), (2,3), (10,10), (10,33), (27,10), (17,19), (31,43), (80,61), (101,3), (1,211), (2,211), (7,809)]
modes = ['1', 'P', 'L', 'LA', 'RGB', 'RGBA', 'RGBX', 'CMYK']
for mode in modes:
    for index, (w,h) in enumerate(dims):
        for pattern in range(3):
            im, pixels = make(mode,w,h,pattern)
            shape = (10,10) if pattern != 2 else ((3,7) if index % 2 else (13,5))
            gray = im.resize(shape).convert('L').tobytes()
            raw_cases.append(dict(name=f'{mode}-{w}x{h}-{pattern}', Mode=mode, Width=w, Height=h,
                                  Pixels=b64(pixels), Palette=b64(palette) if mode == 'P' else '',
                                  shape=shape, gray=b64(gray), hash=tohash(im,shape)))
            if pattern == 0 and mode != 'RGBX':
                formats = ['PNG'] if mode != 'CMYK' else []
                if mode in ('RGB','L','CMYK'):
                    formats.append('JPEG')
                if mode in ('RGB','RGBA','L','P'):
                    formats.append('GIF')
                for fmt in formats:
                    buf = io.BytesIO()
                    im.save(buf, fmt)
                    encoded = buf.getvalue()
                    decoded = Image.open(io.BytesIO(encoded))
                    encoded_cases.append(dict(name=f'{fmt}-{mode}-{w}x{h}', format=fmt, mode=decoded.mode,
                                              encoded=b64(encoded), width=decoded.width, height=decoded.height,
                                              pixels=b64(decoded.tobytes()) if decoded.mode != '1' else '',
                                              gray=b64(decoded.resize((10,10)).convert('L').tobytes()),
                                              hash=tohash(decoded)))

comparison_cases=[]
for a,b,shape in [('', '', (10,10)), ('1'*99,'1'*99,(10,10)), ('1'*101,'1'*101,(10,10)),
                   ('你好a','你好b',(10,10)), ('123','12',(10,10)), ('abc','abc',(-2,3)),
                   ('x'*90+'y'*10, 'x'*100,(10,10)), ('x'*91+'y'*9,'x'*100,(10,10))]:
    comparison_cases.append(dict(a=a,b=b,shape=shape,value=compare(a,b,shape)))

result = dict(pillow=PIL.__version__, jpeg=features.version_codec('jpg'),
              source_sha256=hashlib.sha256(source).hexdigest(),
              methods_ast_sha256=hashlib.sha256(ast.dump(ast.Module(body=methods,type_ignores=[]),include_attributes=False).encode()).hexdigest(),
              pillow_image_sha256=hashlib.sha256(pathlib.Path(Image.__file__).read_bytes()).hexdigest(),
              raw=raw_cases, encoded=encoded_cases, comparison=comparison_cases)
sys.stdout.buffer.write(gzip.compress(json.dumps(result,ensure_ascii=False,separators=(',',':')).encode(),mtime=0))
