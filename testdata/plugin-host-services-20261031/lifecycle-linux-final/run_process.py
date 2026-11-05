#!/usr/bin/env python3
"""Real cmd/server/SDK/filesystem lifecycle, owned synthetic media protocol.

Run with native compiled server and two fixture workers. Python is only this
external driver; neither product nor installed plugin executes Python.
"""
import argparse
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import platform
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request

p=argparse.ArgumentParser()
for key in ["server","worker","worker-v2","directory"]: p.add_argument("--"+key,required=True)
p.add_argument("--hold",action="store_true")
args=p.parse_args()
directory=Path(args.directory).resolve()
directory.mkdir(mode=0o700,parents=True,exist_ok=False)
files=directory/"files";files.mkdir()
(files/"nested").mkdir();(files/"nested"/"child.txt").write_bytes(b"owned child bytes")
(files/"delete.txt").write_bytes(b"owned removable bytes")
data=directory/"data"
platform_id={"Darwin":"darwin","Linux":"linux"}[platform.system()]+"-"+{"arm64":"arm64","aarch64":"arm64","x86_64":"amd64"}[platform.machine()]
guid="11111111111111111111111111111111"
scans=[]
class Media(http.server.BaseHTTPRequestHandler):
    def do_GET(self): self.dispatch()
    def do_POST(self): self.dispatch()
    def dispatch(self):
        raw=self.rfile.read(int(self.headers.get("Content-Length","0")))
        if self.path=="/v/api/v1/sys/version": body={"version":"1.0.0","mediasrvVersion":"1.0.0"}
        elif self.path=="/v/api/v1/login": body={"token":"owned-loopback-session"}
        elif self.path=="/v/api/v1/user/info": body={"is_admin":1}
        elif self.path=="/v/api/v1/mdb/list": body=[dict(guid=guid,name="Owned isolated library",category="Mix",dir_list=["/owned/library/"])]
        elif self.path=="/v/api/v1/task/running": body=True
        elif self.path in ["/v/api/v1/mdb/scan/"+guid,"/v/api/v1/mdb/scanall"]:
            assert not raw, "source refresh must send the empty signed POST"
            scans.append(dict(endpoint=self.path,body_bytes=len(raw)));body=True
        else: self.send_error(404);return
        encoded=json.dumps(dict(code=0,data=body)).encode()
        self.send_response(200);self.send_header("Content-Length",str(len(encoded)));self.end_headers();self.wfile.write(encoded)
    def log_message(self,*_): pass
media=http.server.ThreadingHTTPServer(("127.0.0.1",0),Media)
threading.Thread(target=media.serve_forever,daemon=True).start()
artifacts={};releases=[]
for version,path in [("1.0",args.worker),("2.0",args.worker_v2)]:
    payload=Path(path).resolve().read_bytes()
    manifest=json.loads(subprocess.check_output([str(Path(path).resolve()),"--manifest"]))
    assert manifest["plugin_id"]=="OwnedHostServices" and manifest["version"]==version
    sha=hashlib.sha256(payload).hexdigest();artifacts["/worker-"+version]=payload
    releases.append(dict(version=version,manifest=manifest,artifacts=[dict(platform=platform_id,format="executable",sha256=sha,executable_sha256=sha,size=len(payload),host_contract="light-media.sdk-resources/v1")]))
index=dict(schema_version=1,plugin_id="OwnedHostServices",current_version="1.0",releases=releases)
class Repository(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path=="/index": raw=json.dumps(index).encode()
        elif self.path in artifacts: raw=artifacts[self.path]
        else: self.send_error(404);return
        self.send_response(200);self.send_header("Content-Length",str(len(raw)));self.end_headers();self.wfile.write(raw)
    def log_message(self,*_): pass
repo=http.server.ThreadingHTTPServer(("127.0.0.1",0),Repository)
threading.Thread(target=repo.serve_forever,daemon=True).start()
repository="http://127.0.0.1:"+str(repo.server_port)
for release in releases: release["artifacts"][0]["url"]=repository+"/worker-"+release["version"]
with socket.socket() as sock: sock.bind(("127.0.0.1",0));port=sock.getsockname()[1]
origin="http://127.0.0.1:"+str(port)
bootstrap=dict(username="owned-services",password="owned-services-browser-password",settings=dict(plugin_markets=[repository+"/index"],allowed_roots=[str(files)],fnos=dict(host="http://127.0.0.1:"+str(media.server_port),username="owned-user",password="owned-synthetic-media-password",enabled=True)))
subprocess.run([args.server,"--data-dir",str(data),"--bootstrap-stdin"],input=json.dumps(bootstrap).encode(),check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
log=(directory/"server.log").open("wb")
process=None;observations=[]
def request(method,path,value=None,status=200):
    body=json.dumps(value).encode() if value is not None else None
    req=urllib.request.Request(origin+path,body,{"Content-Type":"application/json"},method=method)
    try: response=client.open(req,timeout=20)
    except urllib.error.HTTPError as e: response=e
    raw=response.read()
    assert response.code==status,(method,path,response.code,status,raw[:500])
    return json.loads(raw) if raw else None
def success(method,path,value=None):
    result=request(method,path,value);assert result["data"]["success"],result;return result["data"]
def start():
    global process
    process=subprocess.Popen([args.server,"--data-dir",str(data),"--listen","127.0.0.1:"+str(port)],stdout=log,stderr=log)
    for _ in range(600):
        try: request("GET","/api/auth/status");break
        except (OSError,AssertionError): assert process.poll() is None,"server exited";time.sleep(.02)
    else: raise AssertionError("server did not start")
    request("POST","/api/auth/login",dict(username=bootstrap["username"],password=bootstrap["password"]))
def stop():
    global process
    if process is not None: process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
def run(method,value,instance="OwnedHostServices",status=200): return request("POST","/api/v1/plugin/"+instance+"/run",dict(method=method,input=value),status=status)
def snapshot(instance="OwnedHostServices"): return request("GET","/api/v1/plugin/"+instance+"/snapshot")
def item(path,kind="dir"): return dict(storage="local",path=str(path),type=kind)
def control(action,instance="OwnedHostServicescopy",**fields):
    result=run("plugin.manager.control",dict(instance_id=instance,action=action,**fields));assert result["accepted"] and result["completed"],result;return result
try:
    start();success("POST","/api/plugins/install",dict(plugin_id="OwnedHostServices",repo_url=repository+"/index"))
    first=snapshot()
    assert run("storage.files.exists",dict(fileitem=item(files))) is True
    flat=run("storage.files.list",dict(fileitem=item(files)))
    assert {entry["name"] for entry in flat}=={"nested","delete.txt"},flat
    recursive=run("storage.files.list",dict(fileitem=item(files),recursion=True))
    assert all(entry["type"]=="file" for entry in recursive) and {entry["name"] for entry in recursive}=={"child.txt","delete.txt"}
    assert run("storage.files.delete",dict(fileitem=item(files/"delete.txt","file"))) is True and not (files/"delete.txt").exists()
    run("storage.files.exists",dict(fileitem=item(directory)),status=502)
    run("storage.files.delete",dict(fileitem=item(files)),status=502)
    success("POST","/api/plugins/OwnedHostServices/clone",dict(suffix="copy"))
    clone=snapshot("OwnedHostServicescopy");assert clone["pid"]!=first["pid"]
    assert run("storage.files.exists",dict(fileitem=item(files/"nested")),instance="OwnedHostServicescopy") is True
    servers=run("media.servers.list",dict(probe=True))
    assert len(servers)==1 and servers[0]["connected"] is True and servers[0]["libraries"][0]["id"]==guid
    assert run("media.servers.refresh",dict(name="fnos",items=[dict(target_path="/owned/library/New")]))["accepted"]
    assert run("media.servers.refresh",dict(name="fnos",root=True))["accepted"]
    assert run("media.servers.refresh_library",dict(name="fnos",library_id=guid))["accepted"]
    assert len(scans)==3
    model=dict(title="Owned process recognized",type="电影",source="themoviedb",tmdb_id=123,year="2024")
    control("init",config=dict(enabled=True,recognized=model))
    recognized=run("media.recognize",dict(mtype="movie",tmdbid=123));assert recognized["title"]==model["title"]
    control("services.remove");control("services.refresh")
    states=run("plugin.manager.read",dict(view="running"));assert len(states)==2 and all(s["loaded"] for s in states)
    control("stop")
    states=run("plugin.manager.read",dict(instance_id="OwnedHostServicescopy"));assert states[0]["loaded"] is False
    control("reload");reloaded=snapshot("OwnedHostServicescopy");assert reloaded["pid"]!=clone["pid"]
    index["current_version"]="2.0"
    success("POST","/api/plugins/install",dict(plugin_id="OwnedHostServices",release_version="2.0",repo_url=repository+"/index",force=True))
    upgraded=snapshot();assert upgraded["version"]=="2.0" and upgraded["pid"]!=first["pid"] and snapshot("OwnedHostServicescopy")["version"]=="1.0"
    before={name:snapshot(name) for name in ["OwnedHostServices","OwnedHostServicescopy"]}
    stop();start()
    after={name:snapshot(name) for name in before}
    for name in before: assert after[name]["pid"]!=before[name]["pid"] and after[name]["last"]==before[name]["last"] and after[name]["config"]==before[name]["config"]
    assert (files/"nested"/"child.txt").read_bytes()==b"owned child bytes"
    assert run("storage.files.exists",dict(fileitem=item(files/"delete.txt","file"))) is False
    observations.append(dict(phase="actual_process_host_services",first=first,clone=clone,reloaded=reloaded,upgraded=upgraded,servers=servers,scans=scans,recognized=recognized,before_restart=before,after_restart=after,source_files_preserved=True,deleted_file_still_absent=True))
    if args.hold:
        state=dict(origin=origin,repository=repository+"/index",directory=str(directory),source="OwnedHostServices",clone="OwnedHostServicescopy")
        (directory/"browser-state.json").write_text(json.dumps(state,indent=2));print(json.dumps(dict(status="browser_ready",**state)),flush=True)
        while not (directory/".browser-done").exists(): assert process.poll() is None;time.sleep(.2)
    control("uninstall")
    request("GET","/api/v1/plugin/OwnedHostServicescopy/snapshot",status=404)
    success("DELETE","/api/plugins/OwnedHostServices")
    request("GET","/api/v1/plugin/OwnedHostServices/snapshot",status=404)
    observations.append(dict(phase="uninstall",apis_removed=True,retained_file_bytes=(files/"nested"/"child.txt").read_bytes().hex()))
    binding={"server":hashlib.sha256(Path(args.server).read_bytes()).hexdigest(),"worker":hashlib.sha256(Path(args.worker).read_bytes()).hexdigest(),"worker_v2":hashlib.sha256(Path(args.worker_v2).read_bytes()).hexdigest(),"driver":hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (directory/"result.json").write_text(json.dumps(dict(status="passed",platform=platform_id,classification="actual cmd/server and standard installed Go workers, real owned files; fnOS is loopback protocol replay; recognition uses actual plugin module override; no existing media/downloader data",binding=binding,observations=observations),indent=2))
    print(json.dumps(dict(status="passed",observations=len(observations))),flush=True)
finally:
    stop();repo.shutdown();media.shutdown();log.close()
