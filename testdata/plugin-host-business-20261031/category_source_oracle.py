#!/usr/bin/env python3
"""Replay the fixed generic Spider category block and ordinary special APIs.

Category fixtures are either the independently captured source projection or
explicitly synthetic data. No site credentials, DB, requests or app imports.
"""
import argparse,ast,copy,hashlib,json,subprocess
from types import SimpleNamespace
from typing import Optional

COMMIT="8a83efeacd3e26b82be495e17d50146e31c96a60"
MEDIA=SimpleNamespace(TV="tv",MOVIE="movie")
def read(repo,path):return subprocess.check_output(["git","-C",repo,"show",COMMIT+":"+path]).decode()
def main():
    p=argparse.ArgumentParser();p.add_argument("--source-repo",required=True);p.add_argument("--catalog",required=True);p.add_argument("--output",required=True);args=p.parse_args()
    path="app/modules/indexer/spider/__init__.py";source=read(args.source_repo,path)
    cls=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef) and n.name=="SiteSpider")
    method=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=="__get_search_url")
    block=next(n for n in ast.walk(method) if isinstance(n,ast.If) and isinstance(n.test,ast.Attribute) and n.test.attr=="category")
    function=ast.FunctionDef(name="project",args=ast.arguments(posonlyargs=[],args=[ast.arg(arg="self"),ast.arg(arg="params")],kwonlyargs=[],kw_defaults=[],defaults=[]),body=[copy.deepcopy(block),ast.Return(value=ast.Name(id="params",ctx=ast.Load()))],decorator_list=[])
    ns={"MediaType":MEDIA};exec(compile(ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[])),COMMIT+":"+path,"exec"),ns)
    catalog=json.load(open(args.catalog));cases=[]
    definitions=[("actual:"+row["source_db_domain"],row.get("category")) for row in catalog["rows"]]
    definitions += [("synthetic:joined",{"movie":[{"id":"401"},{"id":"404"}],"tv":[{"id":"404"},{"id":"402"}],"field":"category","delimiter":","}),("synthetic:null_delimiter",{"movie":[{"id":"401"}],"field":"category","delimiter":None}),("synthetic:integer_join_ID",{"movie":[{"id":401}],"field":"category"}),("synthetic:falsey_values",{"movie":"","tv":None,"field":False}),("synthetic:falsey_field",{"movie":[{"id":401}],"field":False}),("synthetic:missing_id",{"movie":[{}]})]
    for name,definition in definitions:
        for selection in [None,"","401,404","404, 402","unknown"]:
            params={};error=None
            try:returned=ns["project"](SimpleNamespace(category=copy.deepcopy(definition),mtype=None,cat=selection),params)
            except Exception as exc:error=type(exc).__name__;returned=None
            cases.append({"name":name+":"+str(selection),"category":definition,"selection":selection,"initial":{},"params_after":params,"returned":returned,"error_class":error})
    specials={}
    for filename,classname,names,attrs in [("mtorrent.py","MTorrentSpider",{"__get_params"},{"_size","_movie_category","_tv_category"}),("rousi.py","RousiSpider",{"__get_params","__get_category_names_by_ids"},{"_size","_movie_category","_tv_category"})]:
        text=read(args.source_repo,"app/modules/indexer/spider/"+filename);tree=ast.parse(text);source_cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==classname)
        nodes=[copy.deepcopy(n) for n in source_cls.body if isinstance(n,ast.FunctionDef) and n.name in names or isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id in attrs for t in n.targets)]
        namespace={"MediaType":MEDIA,"Optional":Optional};exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name=classname,bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),filename,"exec"),namespace)
        obj=namespace[classname]();get_params=getattr(obj,"_"+classname+"__get_params")
        rows=[]
        for selection in [None,"","2,1"," 1,2","999"]:
            params=get_params("Owned",None,selection,0) if filename=="rousi.py" else get_params("Owned",None,0)
            rows.append({"selection":selection,"expected":params})
        specials[classname]=rows
    result={"source_commit":COMMIT,"classification":"synthetic fixed-source AST block/method replay plus captured ordinary source category data; no real site requests","generic_source":{"path":path,"line":block.lineno,"end_line":block.end_lineno,"sha256":hashlib.sha256("\n".join(source.splitlines()[block.lineno-1:block.end_lineno]).encode()).hexdigest()},"catalog_projection_sha256":catalog["allowed_projection_sha256"],"cases":cases,"specials":specials,"network_calls":0,"production_writes":0}
    with open(args.output,"w") as f:json.dump(result,f,ensure_ascii=False,indent=2);f.write("\n")
if __name__=="__main__":main()
