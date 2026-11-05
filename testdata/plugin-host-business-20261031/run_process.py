#!/usr/bin/env python3
"""Actual isolated cmd/server + installed standard SDK + owned protocol fixture.

The source of historical database rows is explicitly synthetic; this driver
does not claim to download media, use production sites, or create import proofs.
The companion App test creates a real import and verifies record-only deletion.
Python belongs to this test driver, never the server or plugin runtime.
"""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,platform,signal,socket,sqlite3,subprocess,threading,time,urllib.error,urllib.request
from pathlib import Path

def main():
    p=argparse.ArgumentParser();p.add_argument("--server",required=True);p.add_argument("--worker",required=True);p.add_argument("--worker-update");p.add_argument("--directory",required=True);p.add_argument("--hold",action="store_true");args=p.parse_args()
    root=Path(args.directory).resolve();root.mkdir(mode=0o700,parents=True,exist_ok=True);data=root/"data"
    assert not (data/"light-media.db").exists(),"use a new owned test directory"
    target_platform={"Linux":"linux","Darwin":"darwin"}[platform.system()]+"-"+{"x86_64":"amd64","arm64":"arm64","aarch64":"arm64"}[platform.machine()]
    worker=Path(args.worker).resolve();payload=worker.read_bytes();manifest=json.loads(subprocess.check_output([str(worker),"--manifest"]));assert manifest["plugin_id"]=="OwnedHostBusiness"
    sha=hashlib.sha256(payload).hexdigest();index={"schema_version":1,"plugin_id":manifest["plugin_id"],"current_version":manifest["version"],"releases":[{"version":manifest["version"],"manifest":manifest,"artifacts":[{"platform":target_platform,"format":"executable","sha256":sha,"executable_sha256":sha,"size":len(payload),"host_contract":"light-media.sdk-resources/v1"}]}]}
    update_payload=None;update_manifest=None
    if args.worker_update:
        update_worker=Path(args.worker_update).resolve();update_payload=update_worker.read_bytes();update_manifest=json.loads(subprocess.check_output([str(update_worker),"--manifest"]));assert update_manifest["plugin_id"]==manifest["plugin_id"] and update_manifest["version"]!=manifest["version"]
        update_sha=hashlib.sha256(update_payload).hexdigest();assert update_sha!=sha
        index["releases"].append({"version":update_manifest["version"],"manifest":update_manifest,"artifacts":[{"platform":target_platform,"format":"executable","sha256":update_sha,"executable_sha256":update_sha,"size":len(update_payload),"host_contract":"light-media.sdk-resources/v1"}]})
    torrent=b"d4:infod6:lengthi1e4:name4:test12:piece lengthi16384e6:pieces20:"+b"x"*20+b"ee"
    counts={"rss":0,"material":0,"browse":0};fail_rss=False
    class OwnedHTTP(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            nonlocal fail_rss
            from urllib.parse import urlsplit,parse_qs
            path=urlsplit(self.path);query=parse_qs(path.query)
            if path.path=="/index":body=json.dumps(index).encode()
            elif path.path=="/worker":body=payload
            elif path.path=="/worker-update" and update_payload:body=update_payload
            elif path.path=="/rss.php":
                counts["rss"]+=1;assert not self.headers.get("Cookie") and not self.headers.get("Authorization")
                if fail_rss:self.send_error(503);return
                body=b'<rss><channel><item><title>Owned Process RSS</title><description>Owned</description><enclosure url="download.php?id=101&amp;passkey=owned-material-secret" length="1"/><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate></item></channel></rss>'
            elif path.path=="/download.php":
                counts["material"]+=1;assert not self.headers.get("Cookie") and query.get("passkey")==["owned-material-secret"];body=torrent
            elif path.path=="/torrents.php":
                counts["browse"]+=1;assert self.headers.get("Cookie")=="owned-site-cookie"
                body=b'<a href="logout.php">Logout</a><a href="userdetails.php?id=1">Owned</a><table class="torrents"><tr><th>Title</th></tr><tr><td>type</td><td><table class="torrentname"><tr><td class="embedded"><a title="Owned.S01E01.1080p.WEB-DL" href="details.php?id=101">Owned</a></td></tr></table></td><td>1</td><td><span title="2026-10-15 18:30:00">date</span></td><td>1.5<br>GiB</td><td><a href="details.php?id=101#seeders">12</a></td><td>2</td><td>99</td></tr></table>'
            else:self.send_error(404);return
            self.send_response(200);self.send_header("Content-Length",str(len(body)));self.end_headers();self.wfile.write(body)
        def log_message(self,*_):pass
    repository=http.server.ThreadingHTTPServer(("127.0.0.1",0),OwnedHTTP);repo="http://127.0.0.1:"+str(repository.server_port);index["releases"][0]["artifacts"][0]["url"]=repo+"/worker";threading.Thread(target=repository.serve_forever,daemon=True).start()
    if update_manifest:index["releases"][1]["artifacts"][0]["url"]=repo+"/worker-update"
    with socket.socket() as sock:sock.bind(("127.0.0.1",0));port=sock.getsockname()[1]
    origin="http://127.0.0.1:"+str(port)
    bootstrap={"username":"owned-business-acceptance","password":"owned-business-browser-password","settings":{"plugin_markets":[repo+"/index"],"sites":[{"id":"owned-site","name":"Owned synthetic site","kind":"ubits","base_url":repo,"cookie":"owned-site-cookie","rss":repo+"/rss.php?passkey=owned-rss-secret","enabled":False}]}}
    subprocess.run([args.server,"--data-dir",str(data),"--bootstrap-stdin"],input=json.dumps(bootstrap).encode(),check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    # The driver prepares past-state fixtures while the application is stopped.
    # These are owned synthetic rows, never source-service data or claimed native
    # execution evidence. All operations below use the running server's APIs.
    source=root/"source.mkv";destination=root/"target.mkv";source.write_bytes(b"owned synthetic historical media");os.link(source,destination)
    stamp="2026-10-31T00:00:00Z";media={"id":42,"media_type":"movie","title":"Owned history","year":2024}
    subscription={"id":"owned-sub","media":media,"state":"paused","source_state":"S","revision":1,"keyword":"preserve keyword","policy":{"include":"keep","filter_groups":["before"]},"schedule":{"mode":"manual"},"created_at":stamp}
    history={"id":"owned-plan","media":media,"mode":"hardlink","state":"completed","created_at":stamp,"files":[{"id":"owned-history","source":str(source),"target":str(destination),"size":source.stat().st_size,"status":"imported","episodes":[]}]}
    intent={"id":"owned-intent","hash":"a"*40,"media":media,"download_path":str(root),"state":"completed","created_at":stamp}
    with sqlite3.connect(data/"light-media.db") as db:
        for kind,key,value in [("subscription","owned-sub",subscription),("import_plan","owned-plan",history),("intent","owned-intent",intent)]:db.execute("INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)",(kind,key,json.dumps(value),stamp))
        db.execute("INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES('owned-import-job','','import','succeeded',?,0,?,?)",(json.dumps({"id":"owned-plan"}),stamp,stamp))
    jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));process=None;log=(root/"server.log").open("wb");observations=[]
    def request(method,path,value=None,status=200):
        raw=json.dumps(value).encode() if value is not None else None
        req=urllib.request.Request(origin+path,raw,{"Content-Type":"application/json"},method=method)
        try:response=client.open(req,timeout=30)
        except urllib.error.HTTPError as error:response=error
        body=response.read();assert response.code==status,(method,path,response.code,status,body[:500]);return json.loads(body) if body else None
    def success(method,path,value=None):
        result=request(method,path,value);assert result["data"]["success"],result;return result["data"]
    def start():
        nonlocal process
        process=subprocess.Popen([args.server,"--data-dir",str(data),"--listen","127.0.0.1:"+str(port)],stdout=log,stderr=log)
        for _ in range(600):
            try:request("GET","/api/auth/status");break
            except (OSError,AssertionError):assert process.poll() is None;time.sleep(.02)
        else:raise AssertionError("server did not start")
        request("POST","/api/auth/login",{"username":bootstrap["username"],"password":bootstrap["password"]})
    def stop():
        nonlocal process
        if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
    def call(method,value,instance="OwnedHostBusiness",status=200):return request("POST","/api/v1/plugin/"+instance+"/run",{"method":method,"input":value},status)
    def page(instance="OwnedHostBusiness"):return request("GET","/api/plugins/"+instance+"/page")["data"]
    try:
        start();success("POST","/api/plugins/install",{"plugin_id":"OwnedHostBusiness","repo_url":repo+"/index"})
        result=call("settings.write",{"key":"CustomIdentifiers","value":["Owned => Renamed"]});assert result["value"]["changed"] is True
        assert call("settings.read",{"key":"CustomIdentifiers"})["value"]["value"]==["Owned => Renamed"]
        assert call("subscriptions.read",{"id":"owned-sub"})["value"]["items"][0]["keyword"]=="preserve keyword"
        patched=call("subscriptions.update",{"id":"owned-sub","revision":1,"patch":{"sites":["owned-site"],"filter_groups":["after"]}});assert patched["value"]["items"][0]["filter_groups"]==["after"] and patched["value"]["items"][0]["keyword"]=="preserve keyword"
        call("subscriptions.update",{"id":"owned-sub","revision":1,"patch":{"filter_groups":["stale"]}},status=502)
        def native_subscription():
            with sqlite3.connect("file:"+str(data/"light-media.db")+"?mode=ro",uri=True) as db:return json.loads(db.execute("SELECT payload FROM records WHERE kind='subscription' AND id='owned-sub'").fetchone()[0])
        for no_op in [{},{"filter_groups":["after"]}]:call("subscriptions.update",{"id":"owned-sub","revision":2,"patch":no_op});assert native_subscription()["revision"]==2
        null_result=call("subscriptions.update",{"id":"owned-sub","revision":2,"patch":{"sites":None,"filter_groups":None,"keyword":None}})["value"]["items"][0]
        assert null_result["sites"] is None and null_result["filter_groups"] is None and null_result["keyword"] is None
        request("PUT","/api/subscriptions/owned-sub",{"media_type":"movie","tmdb_id":42,"media":media,"state":"paused","revision":3,"site_ids":[],"policy":{"filter_groups":[]},"schedule":{"mode":"manual"}})
        assert not native_subscription().get("source_null_fields") and native_subscription()["revision"]==4
        call("subscriptions.update",{"id":"owned-sub","revision":4,"patch":{"filter_groups":["after"]}})
        extended=call("subscriptions.update",{"id":"owned-sub","revision":5,"patch":{"name":"Owned updated","year":"02025","state":"S","best_version":2,"best_version_full":"2.0","search_imdbid":-1,"manual_total_episode":None,"current_priority":73,"episode_priority":{},"custom_words":"Owned => Pipeline","downloader":""}})["value"]["items"][0]
        assert extended["name"]=="Owned updated" and extended["year"]=="02025" and extended["best_version"]==2 and extended["best_version_full"]==2 and extended["search_imdbid"]==-1 and extended["manual_total_episode"] is None and extended["custom_words"]=="Owned => Pipeline"
        native=native_subscription();assert native["media"]["title"]=="Owned updated" and native["media"]["year"]==2025 and native["best_version"] is True and native["best_version_full"] is True and native["source_search_imdb"] is True and native["state"]=="paused"
        revision=native["revision"];call("subscriptions.update",{"id":"owned-sub","revision":revision,"patch":{"best_version":"2.0"}});assert native_subscription()["revision"]==revision
        assert call("history.download.read",{"hash":"a"*40})["value"]["item"]["id"]=="owned-intent"
        assert call("history.transfer.read",{"source":str(source)})["value"]["item"]["id"]=="owned-history"
        assert call("history.transfer.delete",{"id":"owned-history"})["value"]["changed"] is True
        assert call("history.transfer.read",{"target":str(destination)})["value"]["item"] is None
        assert source.read_bytes()==destination.read_bytes()==b"owned synthetic historical media"
        assert call("sites.read",{})["value"]["items"][0]["id"]=="owned-site"
        assert call("sites.torrents.browse",{"id":"owned-site"})["value"]["items"][0]["id"]=="101"
        rss=call("sites.torrents.rss",{"id":"owned-site"})["value"];assert rss["items"][0]["title"]=="Owned Process RSS"
        assert "passkey" not in json.dumps(rss) and "owned-rss-secret" not in json.dumps(rss)
        fetched=call("sites.torrents.download",{"id":"owned-site","resource_id":rss["items"][0]["id"]})["value"];assert base64.b64decode(fetched["torrent"])==torrent
        fail_rss=True;call("sites.torrents.rss",{"id":"owned-site"},status=502);fail_rss=False
        success("POST","/api/plugins/OwnedHostBusiness/clone",{"suffix":"Twin"})
        clone="OwnedHostBusinesstwin";source_result=call("sites.read",{});clone_result=call("settings.read",{"key":"CustomIdentifiers"},clone);assert source_result["pid"]!=clone_result["pid"]
        before={instance:page(instance) for instance in ("OwnedHostBusiness",clone)}
        update_observed=False
        if update_manifest:
            index["current_version"]=update_manifest["version"]
            success("POST","/api/plugins/install",{"plugin_id":"OwnedHostBusiness","repo_url":repo+"/index","release_version":update_manifest["version"],"force":True})
            assert {instance:page(instance) for instance in before}==before
            with sqlite3.connect("file:"+str(data/"light-media.db")+"?mode=ro",uri=True) as db:
                versions=dict(db.execute("SELECT instance_id,version FROM plugin_installations WHERE instance_id IN ('OwnedHostBusiness','OwnedHostBusinesstwin')").fetchall())
            assert versions=={"OwnedHostBusiness":update_manifest["version"],clone:manifest["version"]},versions
            updated=call("sites.read",{});assert updated["pid"]!=source_result["pid"];source_result=updated
            assert call("settings.read",{"key":"CustomIdentifiers"},clone)["pid"]==clone_result["pid"]
            before={instance:page(instance) for instance in before};update_observed=True
        stop();start()
        assert {instance:page(instance) for instance in before}==before
        cold=call("settings.read",{"key":"CustomIdentifiers"});assert cold["pid"]!=source_result["pid"] and cold["value"]["value"]==["Owned => Renamed"]
        assert call("subscriptions.read",{"id":"owned-sub"})["value"]["items"][0]["filter_groups"]==["after"]
        recovered=call("subscriptions.read",{"id":"owned-sub"})["value"]["items"][0];assert recovered["name"]=="Owned updated" and recovered["best_version"]==2 and recovered["best_version_full"]==2 and recovered["year"]=="02025" and recovered["custom_words"]=="Owned => Pipeline"
        assert call("history.transfer.read",{"source":str(source)})["value"]["item"] is None
        assert source.read_bytes()==destination.read_bytes()==b"owned synthetic historical media"
        observations.append({"phase":"actual_cmd_server_host_resources","source_pid":source_result["pid"],"clone_pid":clone_result["pid"],"cold_pid":cold["pid"],"source_update_new_binary_version_and_pid_clone_unchanged_data_retained":update_observed,"settings_live_and_cold":True,"subscription_patch_and_conflict":True,"subscription_null_noop_native_form_reset":True,"subscription_extended_native_flags_sqlite_affinity_words_metadata_and_cold":True,"history_record_only_retained_two_files":True,"site_browse_rss_material":True,"native_protocol_counts":dict(counts),"errors_observed":True})
        # A pending run_once form lets browser acceptance exercise the normal
        # config-save -> init -> typed resource -> private data-page chain.
        success("PUT","/api/plugins/OwnedHostBusiness",{"marker":"before-browser","method":"settings.read","input":json.dumps({"key":"CustomIdentifiers"}),"run_once":False})
        if args.hold:
            state={"origin":origin,"directory":str(root),"source":"OwnedHostBusiness","clone":clone,"username":bootstrap["username"],"password":bootstrap["password"],"form_method":"settings.read","form_input":json.dumps({"key":"CustomIdentifiers"})}
            (root/"browser-state.json").write_text(json.dumps(state,indent=2));print(json.dumps({"status":"browser_ready",**state}),flush=True)
            while not (root/".browser-done").exists():assert process.poll() is None;time.sleep(.2)
        for instance in (clone,"OwnedHostBusiness"):
            success("DELETE","/api/plugins/"+instance);request("POST","/api/v1/plugin/"+instance+"/run",{},404)
        observations.append({"phase":"uninstall","apis_removed":True,"history_files_unchanged":source.read_bytes()==destination.read_bytes()})
        (root/"result.json").write_text(json.dumps({"status":"passed","platform":target_platform,"classification":"actual cmd/server + standard SDK executable + owned loopback site/artifact protocols; synthetic prepared historical rows, not NAS production site/downloader","observations":observations},indent=2));print(json.dumps({"status":"passed","observations":len(observations)}),flush=True)
    finally:
        stop();repository.shutdown();log.close()

if __name__=="__main__":main()
