from pathlib import Path
import json
base=Path(__file__).resolve().parents[1]
prefix=(Path(__file__).resolve().parents[4]/'testdata/plugins/metainfo-reference/installed_full_metainfo.py').read_text().split('def evaluate(')[0]
body='''
contextmod=load('app/core/context.py',dict(base,MetaInfo=meta.MetaInfo))
identitymod=load('app/utils/media.py')
objects={name:cls() for name,cls in [('MediaInfo',contextmod.MediaInfo),('TorrentInfo',contextmod.TorrentInfo)]}
properties={name:[key for key in dir(cls) if isinstance(getattr(cls,key),property)] for name,cls in [('MediaInfo',contextmod.MediaInfo),('TorrentInfo',contextmod.TorrentInfo)]}
print(json.dumps(dict(source_sha256=sha,defaults={name:vars(obj)for name,obj in objects.items()},properties=properties,casefold={str(n):chr(n).casefold()for n in range(0x110000)if not 0xd800<=n<=0xdfff and chr(n).casefold()!=chr(n)},unicode=unicodedata.unidata_version),ensure_ascii=False,indent=2,default=lambda x:x.value if isinstance(x,Enum)else (_ for _ in ()).throw(TypeError(type(x).__name__))))
'''
(base/'reference/installed_schema.py').write_text(prefix+body)
