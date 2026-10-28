"""Actual fixed FileManager.transfer flag selection with real disposable
source paths and Pydantic directory model. Native handler is a controlled
capture boundary; no scrape/notification side effects are simulated.
"""
import copy,json,tempfile
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
ns=env['ns'];rows=[]
for profile,directory in [('path',None),('directory_default',{}),('directory_false',{'notify':False,'scraping':False}),('directory_true',{'notify':True,'scraping':True}),('directory_null',{'notify':None,'scraping':None})]:
 for scrape in [None,False,True]:
  with tempfile.TemporaryDirectory(prefix='owned-native-flags-')as td:
   root=Path(td);source=root/'Owned.mkv';source.write_bytes(b'owned flags source');target=root/'library';target.mkdir();trace=[]
   conf=None if directory is None else ns['TransferDirectoryConf'](library_path=str(target),library_storage='local',transfer_type='link',**directory)
   class Handler:
    def get_dest_dir(self,**kw):return target
    def get_dest_path(self,**kw):return target
    def transfer_media(self,**kw):trace.append({'need_notify':kw['need_notify'],'need_scrape':kw['need_scrape'],'need_rename':kw['need_rename']});return ns['TransferInfo'](success=True)
   ns['TransHandler']=Handler
   core=ns['FileManagerModule']();core._FileManagerModule__get_storage_oper=lambda storage:object()
   info=ns['MediaInfo']();info.type=SimpleNamespace(value='电影');info.title_year='Owned (2024)'
   result=core.transfer(fileitem=ns['FileItem'](storage='local',type='file',path=str(source)),meta=ns['MetaBase'](),mediainfo=info,target_directory=conf,target_storage='local',target_path=target,transfer_type='link',scrape=scrape,source_oper=object(),target_oper=object())
   assert result.success and len(trace)==1,(profile,scrape,result)
   rows.append(dict(name=profile+'_'+str(scrape),kwargs=dict(target_directory=conf.model_dump(mode='json')if conf else None,scrape=scrape),output=trace[0]))
Path(__file__).with_name('transfer-native-flags-source-oracle.json').write_text(json.dumps(dict(fixed_backend='8a83efeacd3e26b82be495e17d50146e31c96a60',source='app/modules/filemanager/__init__.py:415-541',kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'actual pinned native flag selections')
