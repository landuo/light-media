import hashlib,json,mimetypes,pathlib,signal,sys
signal.alarm(20)
names=['owned'+x for x in ['.png','.jpg','.jpeg','.gif','.webp','.bmp','.ico','.tiff','.heic']]
print(json.dumps({'python':sys.version,'module_sha256':hashlib.sha256(pathlib.Path(mimetypes.__file__).read_bytes()).hexdigest(),'mime':{n:mimetypes.guess_type(n)[0] for n in names}},separators=(',',':')))
