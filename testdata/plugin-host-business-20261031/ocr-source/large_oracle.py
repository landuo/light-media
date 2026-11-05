#!/usr/bin/env python3
"""Owned synthetic PNG and fixed helper output; no recognition/network execution."""
import ast,base64,binascii,hashlib,json,pathlib,struct,subprocess,types,typing,zlib
OUT=pathlib.Path(__file__).resolve().parent;COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/helper/ocr.py']);node=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name=='OcrHelper')
state=0x12345678;rows=[]
for y in range(300):
 row=bytearray([0])
 for x in range(300*3):
  state^=(state<<13)&0xffffffff;state^=state>>17;state^=(state<<5)&0xffffffff;row.append(state&255)
 rows.append(bytes(row))
def chunk(name,data):return struct.pack('>I',len(data))+name+data+struct.pack('>I',binascii.crc32(name+data)&0xffffffff)
png=b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',300,300,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(b''.join(rows)))+chunk(b'IEND',b'')
(OUT/'owned-large.png').write_bytes(png);encoded=base64.b64encode(png).decode();observed=[]
class Request:
 def __init__(self,**kwargs):pass
 def post_res(self,url,json):
  observed.append(dict(url=url,base64_length=len(json['base64_img']),base64_sha256=hashlib.sha256(json['base64_img'].encode()).hexdigest()))
  return types.SimpleNamespace(json=lambda:{'result':'OWNED_LARGE'})
env=dict(vars(typing),base64=base64,settings=types.SimpleNamespace(OCR_HOST='https://owned-ocr.invalid'),RequestUtils=Request)
exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),COMMIT+':app/helper/ocr.py','exec'),env)
result=env['OcrHelper']().get_captcha_text(image_b64=encoded)
(OUT/'large-oracle.json').write_text(json.dumps(dict(source_commit=COMMIT,source_sha256=hashlib.sha256(source).hexdigest(),classification='complete fixed helper AST; owned 300x300 PNG; RequestUtils POST boundary recorded, no OCR challenge/network',png_bytes=len(png),png_sha256=hashlib.sha256(png).hexdigest(),expected_result=result,post=observed[0]),indent=2)+'\n')
print(json.dumps(dict(png_bytes=len(png),base64_bytes=len(encoded),post=result)))
