#!/usr/bin/env python3
"""Compose fixed Douban module + actual response parser for native error cases."""
import copy, importlib.util, json, pathlib

root=pathlib.Path(__file__).parent
def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value);return value
source=load('recognition_http_source',root/'recognition_oracle.py')
http=load('response_http_source',root/'http_oracle.py')
base=json.loads((root/'douban-native-cases.json').read_text())['cases'][0]
cases=[]
for name,broken in [('douban_non_200_complete_json',False),('douban_non_200_people_invalid_json',True)]:
    replies=copy.deepcopy(base['replies'])
    replies[0]['status']=500
    replies[1]['status']=429
    if broken:replies[1]['raw_body']='{';replies[1].pop('body')
    offset=[0]
    def invoke(self,url,**kwargs):
        row=replies[offset[0]];offset[0]+=1
        assert row['path']=='/api/v2'+url
        body=row.get('raw_body',json.dumps(row.get('body')))
        return http.douban._handle_response(http.Response(row['status'],body))
    source.DAPI._DoubanApi__invoke_search=invoke
    module=source.DModule();module.doubanapi=source.DAPI.__new__(source.DAPI)
    try:
        result=module._recognize_media_core(meta=None,mtype=source.base.MediaType.MOVIE,doubanid='901',douban_info_func=module.douban_info,match_doubaninfo_func=module.match_doubaninfo,source='douban')
        expected=result.to_dict() if result else None;error=None
    except Exception as exc:expected=None;error=type(exc).__name__
    assert offset[0]==len(replies)
    cases.append({'case':name,'request':base['request'],'replies':replies,'expected':expected,'error':error})
out={'commit':source.COMMIT,'source_sha256':dict(source.sources,**http.hashes),'kind':'fixed Douban module and actual source response parsing; source native exception becomes None + ordinary system.error at fixed Chain module boundary','cases':cases}
(root/'http-source-native-cases.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'additional_native_cases':len(cases)}))
