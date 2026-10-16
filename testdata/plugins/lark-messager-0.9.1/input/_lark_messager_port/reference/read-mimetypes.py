import base64,hashlib,json,mimetypes,pathlib,signal,sys
signal.alarm(20)
mimetypes.init()
raw=pathlib.Path(mimetypes.__file__).read_bytes()
print(json.dumps({'python':sys.version,'source_sha256':hashlib.sha256(raw).hexdigest(),'source_base64':base64.b64encode(raw).decode(),'types_map':mimetypes.types_map,'common_types':mimetypes.common_types,'suffix_map':mimetypes.suffix_map,'encodings_map':mimetypes.encodings_map},separators=(',',':')))
