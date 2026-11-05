#!/usr/bin/env python3
"""Replay unmodified fixed MoviePilot operator methods with owned model doubles.

This is synthetic source-method replay, not a running MoviePilot service. No
MoviePilot module is imported, no configured credentials or network are used.
"""
import argparse, ast, copy, datetime, hashlib, json, subprocess, threading
from typing import Any, Optional, Union
from enum import Enum

COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"

def read(repo, path):
    return subprocess.check_output(["git", "-C", repo, "show", f"{COMMIT}:{path}"]).decode()

def extract(repo, path, clsname, names, namespace):
    source = read(repo,path)
    cls = next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef) and n.name==clsname)
    nodes = [copy.deepcopy(n) for n in cls.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names]
    for node in nodes: node.decorator_list=[]
    generated = ast.Module(body=[ast.ClassDef(name=clsname,bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])
    exec(compile(ast.fix_missing_locations(generated),f"{COMMIT}:{path}","exec"),namespace)
    return namespace[clsname], {n.name:{"line":n.lineno,"end_line":n.end_lineno,"sha256":hashlib.sha256("\n".join(source.splitlines()[n.lineno-1:n.end_lineno]).encode()).hexdigest()} for n in nodes}

class OwnedClock:
    value=datetime.datetime(2026,10,31,12,0,0)
    @classmethod
    def now(cls): return cls.value

class OwnedRow:
    def __init__(self, **values): self.__dict__.update(values)
    def update(self,db,values): self.__dict__.update(copy.deepcopy(values))
    def create(self,db): db["row"]=self

class OwnedStatistic(OwnedRow):
    @staticmethod
    def get_by_domain(db,domain): return db.get("row")

def sites(repo):
    ns={"datetime":OwnedClock,"Optional":Optional,"SiteStatistic":OwnedStatistic}
    cls,location=extract(repo,"app/db/site_oper.py","SiteOper",{"success","fail"},ns)
    cases=[]
    for name,initial,ok,seconds in [
        ("new_success_without_seconds",None,True,None),
        ("new_success_zero",None,True,0),
        ("new_failure",None,False,None),
        ("existing_success_without_seconds",{"domain":"owned.invalid","success":2,"fail":3,"seconds":7,"lst_state":1,"lst_mod_date":"old","note":{"old":7}},True,None),
        ("same_second_replaces_note",{"domain":"owned.invalid","success":2,"fail":3,"seconds":7,"lst_state":1,"lst_mod_date":"old","note":{"2026-10-31 12:00:00":9}},True,3),
        ("eleventh_note_uses_original_denominator",{"domain":"owned.invalid","success":10,"fail":0,"seconds":8,"lst_state":0,"lst_mod_date":"old","note":{f"2026-10-30 12:00:{i:02d}":i+1 for i in range(10)}},True,11),
        ("failure_preserves_note",{"domain":"owned.invalid","success":2,"fail":3,"seconds":7,"lst_state":0,"lst_mod_date":"old","note":{"old":7}},False,None),
    ]:
        db={}
        if initial is not None: db["row"]=OwnedStatistic(**copy.deepcopy(initial))
        obj=cls();obj._db=db
        if ok: obj.success("owned.invalid",seconds)
        else: obj.fail("owned.invalid")
        cases.append({"name":name,"initial":initial,"success":ok,"seconds":seconds,"clock":"2026-10-31 12:00:00","expected":db["row"].__dict__})
    return {"source":"app/db/site_oper.py","methods":location,"cases":cases}

class Key(Enum): Value="CustomIdentifiers"
class OwnedConfig(OwnedRow):
    @staticmethod
    def get_by_key(db,key): return db.get(key)
    def create(self,db):self.id=1;db[self.key]=self
    def delete(self,db,rid):db.pop(self.key,None)

def settings(repo):
    ns={"Any":Any,"Optional":Optional,"Union":Union,"SystemConfigKey":Key,"SystemConfig":OwnedConfig,"copy":copy}
    cls,location=extract(repo,"app/db/systemconfig_oper.py","SystemConfigOper",{"set","get","all"},ns)
    cases=[]
    for name,initial,next_value in [("absent_empty",None,[]),("absent_values",None,["A => B"]),("same_values",["A => B"],["A => B"]),("replace_values",["A => B"],["C => D"]),("clear_values",["A => B"],[]),("set_null",["A => B"],None)]:
        obj=cls();obj._db={};obj._rlock=threading.RLock();obj._SystemConfigOper__SYSTEMCONF={}
        if initial is not None:
            obj._db["CustomIdentifiers"]=OwnedConfig(key="CustomIdentifiers",value=copy.deepcopy(initial),id=1)
            obj._SystemConfigOper__SYSTEMCONF["CustomIdentifiers"]=copy.deepcopy(initial)
        returned=obj.set(Key.Value,copy.deepcopy(next_value));value=obj.get(Key.Value)
        cases.append({"name":name,"initial":initial,"next":next_value,"expected_return":returned,"expected_value":value,"db_present":"CustomIdentifiers" in obj._db})
    return {"source":"app/db/systemconfig_oper.py","methods":location,"cases":cases}

def main():
    parser=argparse.ArgumentParser();parser.add_argument("--source-repo",required=True);parser.add_argument("--output",required=True);args=parser.parse_args()
    result={"source_commit":COMMIT,"kind":"synthetic fixed-source AST method replay","network_calls":0,"production_writes":0,"sites":sites(args.source_repo),"settings":settings(args.source_repo)}
    with open(args.output,"w") as f:json.dump(result,f,ensure_ascii=False,indent=2);f.write("\n")
if __name__=="__main__":main()
