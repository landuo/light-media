package app

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "light-media/internal/plugins"
 "light-media/internal/plugins/artifacts"
 "net/http"
 "net/http/httptest"
 "os"
 "os/exec"
 "runtime"
 "slices"
 "strings"
 "testing"
 "sync/atomic"
 "time"
)

const ownedSubmittedAppID="OwnedSubmitted"
func ownedSubmittedAppFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_SUBMITTED_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_SUBMITTED_FIXTURE_BINARY must name the actual owned compiled Base SDK worker")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual Base manifest %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != ownedSubmittedAppID || manifest.Version != "1.0" || !slices.Equal(manifest.Capabilities, append(plugins.BaseMethods(),plugins.BackgroundSubmitCapability,plugins.ServicesRefreshCapability)) {
		t.Fatalf("wrong executable/capabilities %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-resources/v1"}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/owned-base-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/owned-base-fixture"
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), ownedSubmittedAppID)
	if err != nil || !in.Installed || in.Version != "1.0" {
		t.Fatalf("actual Base installation %+v %v", in, err)
	}
	value, ok := a.pluginRunningInstances.Load(ownedSubmittedAppID)
	if !ok {
		t.Fatal("real Base worker not loaded")
	}
	object := value.(*originalPluginInstance)
	if !object.external {
		t.Fatal("Base fixture substituted builtin")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("fixture acquisition %d %d %d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
    object.sessionMu.Lock();client:=object.session;object.sessionMu.Unlock()
    if client==nil || !client.SupportsConcurrent(){t.Fatal("installed artifact did not negotiate real concurrency")}
    settings:=a.Settings();settings.APIToken="owned-concurrent-api-key"
    if err:=a.saveSettings(context.Background(),settings);err!=nil{t.Fatal(err)}
    t.Cleanup(func(){
        a.closeOriginalSessions()
        ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
        if err:=client.Stop(ctx);err!=nil{t.Error("actual installed worker exit/race status",err)}
    })
    return a, cookie, object
}

func ownedSubmittedResponse(t *testing.T,r *httptest.ResponseRecorder)(int,bool){
	t.Helper();var v struct{PID int `json:"pid"`;Accepted bool `json:"accepted"`}
	if r.Code!=200 || json.Unmarshal(r.Body.Bytes(),&v)!=nil || v.PID==0 {t.Fatalf("actual submitted HTTP: %d %s",r.Code,r.Body.String())}
	return v.PID,v.Accepted
}
func ownedSubmittedDone(t *testing.T,a *App,id,token string,pid int){
	t.Helper();ownedConcurrentAppWaitData(t,a,id,"completed_"+token)
	row,err:=a.pluginStore.Data(context.Background(),id,"completed_"+token)
	var v struct{PID int `json:"pid"`;OldDenied bool `json:"old_denied"`;Removed struct{ID,Value string} `json:"removed_task"`}
	if err!=nil || json.Unmarshal(row.Value,&v)!=nil || v.PID!=pid || !v.OldDenied || v.Removed.ID!=token || v.Removed.Value!="before-removal"{t.Fatal("fresh authority or copied original argument lost",err,string(row.Value))}
	if _,err=a.pluginStore.Data(context.Background(),id,"unauthorized_parent");err==nil{t.Fatal("retained parent HostCaller succeeded")}
}
func ownedSubmittedClient(p *originalPluginInstance)*plugins.Client {p.sessionMu.Lock();defer p.sessionMu.Unlock();return p.session}

func TestOriginalSubmittedActualHTTPStartsBeforeParentAndSurvivesFailure(t *testing.T){
	a,_,p:=ownedSubmittedAppFixture(t);h:=a.Handler()
	pid,_:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),h,ownedSubmittedAppID,"state","",""))
	for _,mode:=range []string{"normal","error","cancel"}{
		t.Run(mode,func(t *testing.T){
			ctx,cancel:=context.WithCancel(context.Background());defer cancel()
			done:=make(chan *httptest.ResponseRecorder,1)
			go func(){done<-ownedConcurrentAppHTTP(ctx,h,ownedSubmittedAppID,"submit",mode,mode)}()
			ownedConcurrentAppWaitData(t,a,ownedSubmittedAppID,"entered_"+mode)
			if mode=="cancel"{cancel()}
			select{case response:=<-done:
				if mode=="normal"{got,ok:=ownedSubmittedResponse(t,response);if !ok||got!=pid{t.Fatal("actual admission failed",got,ok)}}else if response.Code==200{t.Fatal("parent failure/cancel falsely succeeded")}
			case <-time.After(5*time.Second):t.Fatal("parent waited for child completion")}
			if _,err:=a.pluginStore.Data(context.Background(),ownedSubmittedAppID,"completed_"+mode);err==nil{t.Fatal("child barrier did not hold independently")}
			got,ok:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),h,ownedSubmittedAppID,"release",mode,""));if !ok||got!=pid{t.Fatal("release used replacement process")}
			ownedSubmittedDone(t,a,ownedSubmittedAppID,mode,pid)
		})
	}
	a.wg.Wait();if !originalObjectStillLoaded(p){t.Fatal("parent failure removed loaded object")};ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalSubmittedActualSynchronousRefreshAndGetterRecursion(t *testing.T){
	a,_,p:=ownedSubmittedAppFixture(t)
	for _,item:=range []struct{name,mode string;want bool}{{"first","",true},{"second","recursive",true},{"broken","fail",false}}{
		_,ok:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"refresh",item.name,item.mode));if ok!=item.want{t.Fatal("refresh outcome",item,ok)}
		state:=&a.pluginOriginalDirectJobs;state.mu.Lock();count:=0;var job *originalDirectJob
		for key,value:=range state.jobs{if key.instance==ownedSubmittedAppID{count++;if key.service==item.name{job=value}}};state.mu.Unlock()
		if item.want && (count!=1||job==nil||job.object!=p){t.Fatal("HTTP returned before synchronous exact-object refresh",item,count)}
		if !item.want&&count!=0{t.Fatal("failed getter restored stale services",count)}
	}
	ownedConcurrentAppWaitData(t,a,ownedSubmittedAppID,"recursive_denied")
	_,getter:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"submit","getter","getter"));if !getter{t.Fatal("declared getter submission rejected without source reason")}
	_,lifecycle:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"submit","lifecycle","lifecycle"));if lifecycle{t.Fatal("background resource acquired manager lifecycle authority")}
	a.wg.Wait();ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalSubmittedFullCapacityRejectsBeforeAcceptedAndStops(t *testing.T){
	a,_,p:=ownedSubmittedAppFixture(t);client:=ownedSubmittedClient(p)
	for i:=0;i<31;i++{_,accepted:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"submit",fmt.Sprint(i),""));if !accepted{t.Fatal("unexpected submit failure",i)}}
	// Thirty-one children plus this actual parent occupy all ordinary slots.
	ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
	_,accepted:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(ctx,a.Handler(),ownedSubmittedAppID,"submit","overflow",""));if accepted{t.Fatal("full SDK table reported queued acceptance")}
	if _,err:=a.pluginStore.Data(context.Background(),ownedSubmittedAppID,"entered_overflow");err==nil{t.Fatal("rejected submission reached worker")}
	if err:=a.stopOriginalPlugin(context.Background(),ownedSubmittedAppID);err!=nil{t.Fatal(err)};a.wg.Wait()
	if err:=client.Stop(context.Background());err!=nil{t.Fatal("actual child not reaped cleanly",err)}
	row,err:=a.pluginStore.Data(context.Background(),ownedSubmittedAppID,"cleanup_stop");var v struct{Order []string `json:"order"`};if err!=nil||json.Unmarshal(row.Value,&v)!=nil||strings.Join(v.Order,",")!="close,stop_service,stop"{t.Fatal("manager stop order",err,string(row.Value))}
	count:=0;a.pluginSubmittedObjects.Range(func(_, _ any)bool{count++;return true});if count!=0{t.Fatal("submission owner registry retained completed leases",count)};ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalSubmittedFailedInitializationKeepsOnlyAcceptedObject(t *testing.T){
	for _,ending:=range []string{"complete","stop","app-close"}{t.Run(ending,func(t *testing.T){
		a,_,p:=ownedSubmittedAppFixture(t)
		if _,err:=p.callOriginalMethod(context.Background(),"configure",json.RawMessage(`{"submit_init":true,"fail_init":true}`));err!=nil{t.Fatal(err)}
		loaded,err:=a.reloadOriginalPlugin(context.Background(),ownedSubmittedAppID);if err!=nil||loaded!=nil{t.Fatal("failed initializer was published",loaded,err)}
		if _,ok:=a.pluginRunningInstances.Load(ownedSubmittedAppID);ok{t.Fatal("failed object leaked into running registry")}
		var held *originalPluginInstance;a.pluginSubmittedObjects.Range(func(key,_ any)bool{candidate:=key.(*originalPluginInstance);if candidate.installation.InstanceID==ownedSubmittedAppID{if held!=nil{t.Error("multiple unexpected held objects")};held=candidate};return true})
		if held==nil||held==p{t.Fatal("accepted task did not hold its exact new initializer object")};client:=ownedSubmittedClient(held);if client==nil{t.Fatal("failed init killed accepted task")}
		row,err:=a.pluginStore.Data(context.Background(),ownedSubmittedAppID,"entered_init");var entered struct{PID int `json:"pid"`};if err!=nil||json.Unmarshal(row.Value,&entered)!=nil{t.Fatal("actual init child entry absent",err)}
		switch ending {
		case "complete":
			// Fixture-only barrier release in owned storage. The worker still
			// performs its real Base read/write with the new held-object scope.
			if err:=a.pluginStore.originalBaseSaveData(context.Background(),ownedSubmittedAppID,"release_init",json.RawMessage(`true`));err!=nil{t.Fatal(err)}
			ownedSubmittedDone(t,a,ownedSubmittedAppID,"init",entered.PID);a.wg.Wait()
		case "stop":
			if err:=a.stopOriginalPlugin(context.Background(),ownedSubmittedAppID);err!=nil{t.Fatal(err)};a.wg.Wait()
		case "app-close":
			if err:=a.Close();err!=nil{t.Fatal(err)}
		}
		ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel();if err:=client.Stop(ctx);err!=nil{t.Fatal("held object transport not joined/reaped cleanly",err)}
		count:=0;a.pluginSubmittedObjects.Range(func(_, _ any)bool{count++;return true});if count!=0{t.Fatal("held object lease survived completion/shutdown",count)}
		verifyStore,verifyPlugins:=a.Store,a.pluginStore
		if ending=="app-close"{
			verifyStore,err=OpenStore(a.Dir);if err!=nil{t.Fatal(err)};defer verifyStore.Close()
			verifyPlugins,err=NewPluginStore(context.Background(),verifyStore);if err!=nil{t.Fatal(err)}
		}
		for _,hook:=range []string{"close","stop_service","stop"}{
			// The prior loaded object's explicit reload stop already wrote
			// markers. No marker may identify the failed initializer PID.
			row,err:=verifyPlugins.Data(context.Background(),ownedSubmittedAppID,"cleanup_"+hook)
			var mark struct{PID int `json:"pid"`}
			if err==nil&&json.Unmarshal(row.Value,&mark)==nil&&mark.PID==entered.PID{t.Fatal("unpublished retirement invoked extra business hook",hook)}
		}
		var runs int;if err:=verifyStore.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs);err!=nil||runs!=0{t.Fatal("retired queue used",runs,err)}
	})}
}

func TestOriginalSubmittedCloneHasIndependentTasksAndResources(t *testing.T){
	a,cookie,source:=ownedSubmittedAppFixture(t)
	r:=callTest(t,a.Handler(),"POST","/api/plugins/"+ownedSubmittedAppID+"/clone",`{"suffix":"Twin"}`,cookie,"");assertOriginalRegistrySuccess(t,r.Code,r.Body.Bytes());a.wg.Wait()
	cloneID:=ownedSubmittedAppID+"twin";value,ok:=a.pluginRunningInstances.Load(cloneID);if !ok{t.Fatal("actual clone absent")};clone:=value.(*originalPluginInstance)
	client:=ownedSubmittedClient(clone);t.Cleanup(func(){_ = a.stopOriginalPlugin(context.Background(),cloneID);if err:=client.Stop(context.Background());err!=nil{t.Error("clone child exit/race",err)}})
	first,ok:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"submit","same",""));if !ok{t.Fatal("source submit")}
	second,ok:=ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),cloneID,"submit","same",""));if !ok||first==second||source==clone{t.Fatal("clone shared object",first,second)}
	_,_ = ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),cloneID,"release","same",""));ownedSubmittedDone(t,a,cloneID,"same",second)
	if _,err:=a.pluginStore.Data(context.Background(),ownedSubmittedAppID,"completed_same");err==nil{t.Fatal("clone released source task")}
	_,_ = ownedSubmittedResponse(t,ownedConcurrentAppHTTP(context.Background(),a.Handler(),ownedSubmittedAppID,"release","same",""));ownedSubmittedDone(t,a,ownedSubmittedAppID,"same",first);a.wg.Wait();ownedConcurrentAppNoRuns(t,a)
}

