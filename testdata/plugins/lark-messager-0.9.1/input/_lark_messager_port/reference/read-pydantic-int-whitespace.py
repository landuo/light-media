# Complete scalar probe of the actual installed integer validator; synthetic only.
import json,pydantic_core,signal,sys
signal.alarm(40)
v=pydantic_core.SchemaValidator({'type':'int'})
accepted=[]
for i in range(0x110000):
 c=chr(i)
 try:
  if v.validate_python(c+'2'+c)==2:accepted.append(i)
 except (ValueError,TypeError):pass
print(json.dumps({'version':pydantic_core.__version__,'python':sys.version,'probe':'validate_python(c+"2"+c)==2','accepted':accepted},separators=(',',':')))
