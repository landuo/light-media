#!/usr/bin/env python3
# Auxiliary evidence only: this runner executes AST-extracted original snippets,
# not complete module imports or the ordinary installed plugin/Base startup chain.
# Real source-component acceptance uses the separately bound independent complete
# leaf-file spec-import runner; that too does not establish parent/helper startup.
"""Fresh fixed-source wrapper + actual vendor + owned Transmission oracle.

Run ONLY inside the documented 20261030 isolated-daemons harness. No daemon
credentials, runtime config, or databases are emitted. Synthetic task bodies
have no trackers, are private, and are never deleted by this driver.
"""
import ast
import hashlib
import importlib.metadata
import json
import logging
import os
import pathlib
import subprocess
import sys
import time
import types
from typing import Optional, Union, Tuple, List
from urllib.parse import urlsplit

import transmission_rpc
from transmission_rpc import Client, Torrent, File
from transmission_rpc.session import Session, SessionStats

logging.disable(logging.CRITICAL)
source, installed, output = map(pathlib.Path, sys.argv[1:4])
assert not output.exists(), "refusing to overwrite oracle"
assert os.environ.get("DOWNLOADER_TEST_INSTANCE"), "owned daemon harness required"
url = urlsplit(os.environ["TRANSMISSION_URL"])
assert url.hostname == "127.0.0.1" and url.scheme == "http" and url.path == "/transmission/rpc"
run = pathlib.Path(os.environ["DOWNLOADER_TEST_INSTANCE"])
raw = (source / "app/modules/transmission/transmission.py").read_bytes()
tree = ast.parse(raw)
definition = next(x for x in tree.body if isinstance(x, ast.ClassDef) and x.name == "Transmission")
null_log = types.SimpleNamespace(**{name: lambda *a, **k: None for name in ("debug", "info", "warning", "error")})
env = dict(Optional=Optional, Union=Union, Tuple=Tuple, List=List, transmission_rpc=transmission_rpc, Client=Client, Torrent=Torrent, File=File, Session=Session, SessionStats=SessionStats, logger=null_log)
exec(compile(ast.fix_missing_locations(ast.Module(body=[definition], type_ignores=[])), str(source / "app/modules/transmission/transmission.py"), "exec"), env)
wrapper = env["Transmission"](host=url.hostname, port=url.port, protocol="http", username=os.environ["TRANSMISSION_USERNAME"], password=os.environ["TRANSMISSION_PASSWORD"])
assert wrapper.trc is not None
client = wrapper.trc
if len(sys.argv)>4 and sys.argv[4]=="--query-only":
    request=json.load(sys.stdin)
    rows,error=wrapper.get_torrents(**request)
    result={"torrents":[dict(fields=row.fields) for row in rows],"error":error,"source_sha256":hashlib.sha256(raw).hexdigest()}
    output.parent.mkdir(parents=True,exist_ok=True)
    output.write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
    print(json.dumps({"source_query_only":True,"sha256":hashlib.sha256(output.read_bytes()).hexdigest()}))
    sys.exit(0)
session_before = client.get_session().fields
assert session_before["rename-partial-files"] is True

def bencode(value):
    if isinstance(value, int): return b"i" + str(value).encode() + b"e"
    if isinstance(value, str): value = value.encode()
    if isinstance(value, bytes): return str(len(value)).encode() + b":" + value
    if isinstance(value, list): return b"l" + b"".join(map(bencode, value)) + b"e"
    return b"d" + b"".join(bencode(k) + bencode(value[k]) for k in sorted(value)) + b"e"

tasks = []
for i, (name, present, labels) in enumerate([
    ("S01-query-complete.bin", True, ["alpha", "双语"]),
    ("S01-query-missing.bin", False, ["alpha", "beta"]),
]):
    data = bytes([65+i])*32768
    info = {"name":name, "length":len(data), "piece length":16384, "pieces":b"".join(hashlib.sha1(data[n:n+16384]).digest() for n in range(0,len(data),16384)), "private":1}
    body = bencode({"info":info})
    if present: (run/"data"/name).write_bytes(data)
    torrent = client.add_torrent(body, download_dir=str(run/"data"), paused=True, labels=labels)
    client.verify_torrent(torrent.id)
    deadline = time.monotonic()+15
    while True:
        current = client.get_torrent(torrent.id)
        if current.status.stopped and current.percent_done == int(present): break
        assert time.monotonic()<deadline, "owned verification did not settle"
        time.sleep(.05)
    tasks.append({"id":torrent.id,"hash":torrent.hashString,"name":name,"present":present,"torrent_sha256":hashlib.sha256(body).hexdigest(),"data_sha256":hashlib.sha256(data).hexdigest()})

phase=os.environ.get("TRANSMISSION_QUERY_PHASE","paused")
if phase=="active":
    for task in tasks: client.start_torrent(task["id"])
    deadline=time.monotonic()+15
    while True:
        states=[str(client.get_torrent(task["id"]).status) for task in tasks]
        if states==["seeding","downloading"]: break
        assert time.monotonic()<deadline,"owned active state did not settle"
        time.sleep(.05)
    time.sleep(1.1)
assert phase in ("paused","active")
cases = []
def collect(name, **kwargs):
    rows, error = wrapper.get_torrents(**kwargs)
    cases.append({"name":name,"request":dict(downloader_id="owned-tr", **kwargs),"error":error,"torrents":[dict(fields=row.fields) for row in rows]})

collect("nil-all")
collect("empty-ids-means-all", ids=[])
collect("hash", ids=tasks[0]["hash"])
collect("integer", ids=tasks[1]["id"])
collect("mixed-ids", ids=[tasks[1]["id"],tasks[0]["hash"]])
collect("duplicate-id", ids=[tasks[0]["id"],tasks[0]["id"]])
collect("missing-hash", ids="f"*40)
collect("uppercase-hash-source-error", ids=tasks[0]["hash"].upper())
collect("sha256-source-error", ids="a"*64)
collect("negative-id-source-error", ids=-1)
collect("fractional-id-source-error", ids=1.5)
collect("list-recently-active-source-error", ids=["recently-active"])
collect("empty-status-tags", status=[], tags=[])
collect("stopped", status="stopped")
collect("seeding", status="seeding")
collect("downloading", status="downloading")
collect("nonmatching-status", status=["seeding","downloading"])
collect("literal-underscore-status", status="download_pending")
collect("alpha-tag", tags="alpha")
collect("tag-subset", tags=["alpha","beta"])
collect("comma-tag-subset", tags="alpha,beta")
collect("input-tag-space-not-trimmed", tags="alpha, beta")
collect("unicode-tag", tags="双语")
collect("combined", ids=[tasks[0]["hash"]], status=["stopped"], tags=["alpha","双语"])

for method_name in ("get_completed_torrents","get_downloading_torrents"):
    for args in ({}, {"ids":[tasks[0]["hash"]],"tags":"alpha"}):
        rows=getattr(wrapper,method_name)(**args)
        cases.append({"name":method_name+("-selected" if args else "-all"),"request":dict(downloader_id="owned-tr",query_method=method_name,**args),"error":rows is None,"torrents":[dict(fields=row.fields) for row in rows or []]})

# Execute only an AST-extracted original ordinary consumer method snippet.
# This does not import its module or initialize the original plugin/Base/helpers.
plugin_path = installed/"cleaninvalidseed/__init__.py"
plugin_raw = plugin_path.read_bytes()
plugin_tree = ast.parse(plugin_raw)
plugin_cls = next(x for x in plugin_tree.body if isinstance(x, ast.ClassDef) and x.name == "CleanInvalidSeed")
method = next(x for x in plugin_cls.body if isinstance(x, ast.FunctionDef) and x.name == "get_all_torrents")
plugin_env = dict(logger=null_log)
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name="OriginalConsumer", bases=[], keywords=[], decorator_list=[], body=[method])], type_ignores=[])), str(plugin_path), "exec"), plugin_env)
consumer = plugin_env["OriginalConsumer"](); consumer._notify=False
ordinary = consumer.get_all_torrents(types.SimpleNamespace(name="Owned Transmission", instance=wrapper))
assert [row.fields for row in ordinary] == [row["fields"] for row in cases[0]["torrents"]]
wrong=env["Transmission"](host=url.hostname,port=url.port,protocol="http",username=os.environ["TRANSMISSION_USERNAME"],password="owned-deliberately-invalid")
wrong_rows,wrong_error=wrong.get_torrents()
assert wrong_rows==[] and wrong_error is True
assert wrong.get_completed_torrents() is None and wrong.get_downloading_torrents() is None
session_after = client.get_session().fields
assert session_after["rename-partial-files"] == session_before["rename-partial-files"]
assert all(client.get_torrent(row["id"]).peers == [] for row in tasks)
# Legacy output wording is preserved for byte-stable execution: "wrapper" means
# the class snippet and "ordinary_consumer" means the method snippet, never full
# source-module/plugin initialization. Existing oracle/log bytes are not rewritten.
report = {"phase":phase,"wrong_auth":{"torrents":[],"error":wrong_error,"completed":None,"downloading":None},"source_commit":"8a83efeacd3e26b82be495e17d50146e31c96a60","source_files":{"app/modules/transmission/transmission.py":hashlib.sha256(raw).hexdigest(),"installed/cleaninvalidseed/__init__.py":hashlib.sha256(plugin_raw).hexdigest()},"dependencies":{n:importlib.metadata.version(n) for n in ("transmission-rpc","requests","urllib3")},"server_version":client.get_session().version,"tasks":tasks,"cases":cases,"ordinary_consumer":{"method":"CleanInvalidSeed.get_all_torrents","line":method.lineno,"full_fields_equal":True},"boundary":"Actual fixed wrapper constructor/query plus actual ordinary consumer method and vendor on fresh owned daemon; not the complete original application's service registration."}
output.parent.mkdir(parents=True,exist_ok=True)
output.write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n")
print(json.dumps({"source_cases":len(cases),"ordinary_consumer_equal":True,"real_daemon":True,"zero_peers":True,"oracle_sha256":hashlib.sha256(output.read_bytes()).hexdigest()}))
if len(sys.argv)>4:
    child_env=os.environ.copy();child_env["TRANSMISSION_QUERY_ORACLE"]=str(output.resolve());child_env["TRANSMISSION_QUERY_SOURCE_ROOT"]=str(source.resolve());child_env["TRANSMISSION_QUERY_INSTALLED_ROOT"]=str(installed.resolve());child_env["TRANSMISSION_QUERY_EVIDENCE_DIR"]=str(output.parent/(output.stem+"-queries"))
    status=subprocess.run(sys.argv[4:], env=child_env).returncode
    peer_counts=[len(client.get_torrent(row["id"]).peers) for row in tasks]
    assert peer_counts==[0,0]
    retained=[]
    for row in tasks:
        path=run/"data"/row["name"]
        if row["present"]:
            digest=hashlib.sha256(path.read_bytes()).hexdigest()
            assert digest==row["data_sha256"]
            retained.append({"name":row["name"],"sha256":digest})
    client.stop_torrent([row["id"] for row in tasks])
    postflight={"target_exit":status,"zero_peers_after_target":True,"source_data_retained":retained,"only_new_owned_tasks_stopped":True}
    output.with_name(output.stem+"-postflight.json").write_text(json.dumps(postflight,indent=2)+"\n")
    sys.exit(status)
