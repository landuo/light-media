#!/usr/bin/env python3
import ast, copy, hashlib, json, pathlib, re, subprocess
from typing import Optional, Union
from zhconv_rs import zhconv

ROOT=pathlib.Path(__file__).resolve().parents[3]
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path): return subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True)
tmdb=source('app/modules/themoviedb/tmdbapi.py');string=source('app/utils/string.py')
class MediaType: MOVIE='电影'; TV='电视剧'
ns={'Optional':Optional,'Union':Union,'re':re,'MediaType':MediaType,'zhconv_convert':zhconv}
for text,name,alias in [(string,'is_chinese','is_chinese'),(tmdb,'__update_tmdbinfo_cn_title','update_title')]:
    node=next(n for n in ast.walk(ast.parse(text)) if isinstance(n,ast.FunctionDef) and n.name==name)
    node=copy.deepcopy(node);node.name=alias;node.decorator_list=[]
    exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'fixed-source/'+name,'exec'),ns)
    if alias=='is_chinese': ns['StringUtils']=type('StringUtils',(),{'is_chinese':staticmethod(ns[alias])})
cases=[]
specs=[('Owned',[],None),('原中文',[('CN','另一名')],'新加坡名'),('Owned',[('CN','繁體名'),('CN','简体名')],'新加坡名'),('Owned',[('TW','简体名'),('CN','電腦')],'新加坡名'),('Owned',[('CN','乾隆')],'新加坡名'),('Owned',[('CN','理髮')],None),('Owned',[('CN','简体名'),('CN','另一名')],None),('Owned',[('CN','繁體-{=>zh-hans:甲}-')],None),('Owned',[('CN','-{R|繁體}-')],'新加坡名'),('Owned',[('CN','Not Chinese')],'English')]
for kind in ['movie','tv']:
    key,alts,original=('title','titles','original_title') if kind=='movie' else ('name','results','original_name')
    for title,aliases,sg in specs:
        info={'id':88001,key:title,original:'Owned original','media_type':'电影' if kind=='movie' else '电视剧','original_language':'en','alternative_titles':{alts:[{'iso_3166_1':r,'title':s} for r,s in aliases]},'translations':{'translations':[]},'genres':[],'credits':{'cast':[],'crew':[]},'external_ids':{}}
        if sg is not None: info['translations']['translations']=[{'iso_3166_1':'SG','data':{key:sg}}]
        before=copy.deepcopy(info); info['sg_title']=sg
        row={'kind':kind,'input':before}
        try:ns['update_title'](info);row['title']=info[key]
        except BaseException as e:row['error']=type(e).__name__
        cases.append(row)
out={'commit':COMMIT,'source_sha256':{'tmdbapi.py':hashlib.sha256(tmdb.encode()).hexdigest(),'string.py':hashlib.sha256(string.encode()).hexdigest()},'cases':cases}
(pathlib.Path(__file__).parent/'title-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'errors':sum('error'in c for c in cases)}))
