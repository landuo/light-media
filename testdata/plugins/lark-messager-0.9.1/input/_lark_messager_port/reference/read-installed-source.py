import base64,hashlib,importlib.metadata,json,pathlib,signal,sys
signal.alarm(45)
root=pathlib.Path('/app/app/plugins/larkmessager')
files={}
for name in ['__init__.py','client.py','crypto.py','schemas.py','README.md','CHANGELOG.md']:
 data=(root/name).read_bytes(); files[name]={'sha256':hashlib.sha256(data).hexdigest(),'base64':base64.b64encode(data).decode()}
versions={}
for package in ['requests','fastapi','pydantic','pycryptodome','cryptography']:
 try: versions[package]=importlib.metadata.version(package)
 except importlib.metadata.PackageNotFoundError: versions[package]=None
print(json.dumps({'files':files,'versions':versions,'python':sys.version}))
