from pathlib import Path
import hashlib,json
files=['app/utils/media.py','app/utils/string.py']
print(json.dumps({p:dict(source=(Path('/app')/p).read_text(),sha256=hashlib.sha256((Path('/app')/p).read_bytes()).hexdigest()) for p in files}))
