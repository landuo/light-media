# Offline fixed-runtime predicate table; reads no application state.
import json,signal,sys,unicodedata
signal.alarm(30)
ranges=[];start=None
for i in range(0x110000):
 v=chr(i).isprintable()
 if v and start is None:start=i
 if not v and start is not None:ranges.append([start,i-1]);start=None
if start is not None:ranges.append([start,0x10ffff])
print(json.dumps({'python':sys.version,'unicode':unicodedata.unidata_version,'predicate':'str.isprintable','ranges':ranges},separators=(',',':')))
