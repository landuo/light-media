import ast,base64,hashlib,json,pathlib,signal
signal.alarm(45)
root=pathlib.Path('/app/app')
paths=['schemas/__init__.py','schemas/message.py','schemas/types.py','plugins/__init__.py','chain/message.py']
files={}
for name in paths:
 path=root/name
 if path.exists():
  b=path.read_bytes();files[name]={'sha256':hashlib.sha256(b).hexdigest(),'base64':base64.b64encode(b).decode()}
print(json.dumps(files))
