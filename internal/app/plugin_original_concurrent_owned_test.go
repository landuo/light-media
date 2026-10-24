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
    "net/url"
    "os"
    "os/exec"
    "runtime"
    "slices"
    "strings"
    "sync/atomic"
    "testing"
    "time"
)

const ownedConcurrentAppID="OwnedAppConcurrent"
func ownedConcurrentAppFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_APP_CONCURRENT_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_APP_CONCURRENT_FIXTURE_BINARY must name the actual owned compiled Base SDK worker")
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
	if manifest.PluginID != ownedConcurrentAppID || manifest.Version != "1.0" || !slices.Equal(manifest.Capabilities, plugins.BaseMethods()) {
		t.Fatalf("wrong executable/capabilities %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
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
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), ownedConcurrentAppID)
	if err != nil || !in.Installed || in.Version != "1.0" {
		t.Fatalf("actual Base installation %+v %v", in, err)
	}
	value, ok := a.pluginRunningInstances.Load(ownedConcurrentAppID)
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

type ownedConcurrentAppState struct{PID int `json:"pid"`;InitCount int `json:"init_count"`;Path string `json:"path"`;Token string `json:"token"`;Value json.RawMessage `json:"value"`}
func ownedConcurrentAppSnapshot(t *testing.T,p *originalPluginInstance)ownedConcurrentAppState{
    t.Helper();var out ownedConcurrentAppState
    ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
    if err:=p.readSession(ctx,"snapshot",&out);err!=nil{t.Fatal(err)}
    if out.PID==0{t.Fatal("missing actual worker PID")};return out
}
func ownedConcurrentAppHTTP(ctx context.Context,h http.Handler,id,endpoint,token,group string)*httptest.ResponseRecorder{
    query:=url.Values{"apikey":{"owned-concurrent-api-key"},"token":{token},"group":{group}}
    request:=httptest.NewRequest("GET","/api/v1/plugin/"+id+"/"+endpoint+"?"+query.Encode(),nil).WithContext(ctx)
    response:=httptest.NewRecorder();h.ServeHTTP(response,request);return response
}
func ownedConcurrentAppResult(t *testing.T,r *httptest.ResponseRecorder)ownedConcurrentAppState{
    t.Helper();var out ownedConcurrentAppState
    if r.Code!=http.StatusOK || json.Unmarshal(r.Body.Bytes(),&out)!=nil || out.PID==0{t.Fatalf("actual plugin HTTP response: %d %s",r.Code,r.Body.String())};return out
}
func ownedConcurrentAppWaitData(t *testing.T,a *App,instance,key string){
    t.Helper();ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
    for {if _,err:=a.pluginStore.Data(ctx,instance,key);err==nil{return};select{case <-time.After(5*time.Millisecond):case <-ctx.Done():t.Fatal("actual SDK entry write not observed",key)}}
}
func ownedConcurrentAppNoRuns(t *testing.T,a *App){t.Helper();var count int;if err:=a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&count);err!=nil || count!=0{t.Fatal("concurrent standard call entered retired run queue",count,err)}}

func TestOriginalConcurrentAppHTTPOverlapCancellationAndObjectIdentity(t *testing.T){
    a,_,object:=ownedConcurrentAppFixture(t);h:=a.Handler()
    before:=ownedConcurrentAppSnapshot(t,object)
    ctx,cancel:=context.WithTimeout(context.Background(),8*time.Second);defer cancel()
    responses:=make(chan *httptest.ResponseRecorder,2)
    for _,token:=range []string{"left","right"}{token:=token;go func(){responses<-ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"barrier",token,"same-object")}()}
    seen:=map[string]bool{}
    for i:=0;i<2;i++{select{case response:=<-responses:state:=ownedConcurrentAppResult(t,response);if state.PID!=before.PID || seen[state.Token]{t.Fatal("HTTP handlers did not share one persistent object with distinct results",state)};seen[state.Token]=true;case <-ctx.Done():t.Fatal("actual HTTP calls were serialized at App or SDK boundary")}}
    if !seen["left"] || !seen["right"]{t.Fatal("interleaved results crossed calls",seen)}
    leftCtx,cancelLeft:=context.WithCancel(ctx);defer cancelLeft()
    leftDone:=make(chan *httptest.ResponseRecorder,1);rightDone:=make(chan *httptest.ResponseRecorder,1)
    go func(){leftDone<-ownedConcurrentAppHTTP(leftCtx,h,ownedConcurrentAppID,"hold","cancel-left","")}()
    go func(){rightDone<-ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"hold","keep-right","")}()
    ownedConcurrentAppWaitData(t,a,ownedConcurrentAppID,"entered_cancel-left")
    ownedConcurrentAppWaitData(t,a,ownedConcurrentAppID,"entered_keep-right")
    cancelLeft()
    select{case response:=<-leftDone:if response.Code==http.StatusOK{t.Fatal("canceled HTTP call fabricated success")};case <-time.After(2*time.Second):t.Fatal("caller cancellation did not reach real worker")}
    release:=ownedConcurrentAppResult(t,ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"release","keep-right",""))
    if release.PID!=before.PID{t.Fatal("release restarted object")}
    select{case response:=<-rightDone:state:=ownedConcurrentAppResult(t,response);if state.PID!=before.PID || state.Token!="keep-right"{t.Fatal("single cancel corrupted sibling result",state)};case <-ctx.Done():t.Fatal("sibling call remained blocked")}
    fresh:=ownedConcurrentAppResult(t,ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"state","after-cancel",""))
    if fresh.PID!=before.PID{t.Fatal("worker replaced after single cancellation")}
    ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalConcurrentAppFullCapacityManagerStop(t *testing.T){
    a,_,object:=ownedConcurrentAppFixture(t)
    before:=ownedConcurrentAppSnapshot(t,object)
    ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
    for i:=0;i<32;i++{raw,_:=json.Marshal(map[string]string{"token":fmt.Sprintf("slot-%02d",i)});a.broadcastOriginalPluginEvent(ctx,"owned.concurrent",raw)}
    for i:=0;i<32;i++{ownedConcurrentAppWaitData(t,a,ownedConcurrentAppID,fmt.Sprintf("entered_slot-%02d",i))}
    // These event handlers ignore cancellation until real source close releases
    // them. No mock host, test semaphore, or manually edited job is involved.
    object.sessionMu.Lock();client:=object.session;object.sessionMu.Unlock()
    stopCtx,stopCancel:=context.WithTimeout(context.Background(),5*time.Second);defer stopCancel()
    if err:=a.stopOriginalPlugin(stopCtx,ownedConcurrentAppID);err!=nil{t.Fatal(err)}
    a.wg.Wait()
    if err:=client.Stop(stopCtx);err!=nil{t.Fatal("actual manager did not join/reap clean worker",err)}
    if _,ok:=a.pluginRunningInstances.Load(ownedConcurrentAppID);ok{t.Fatal("stopped object remains registered")}
    for _,method:=range []string{"close","stop_service","stop"}{
        row,err:=a.pluginStore.Data(context.Background(),ownedConcurrentAppID,"cleanup_"+method)
        var value struct{PID int `json:"pid"`;Order []string `json:"order"`;OldDenied bool `json:"old_denied"`}
        if err!=nil || json.Unmarshal(row.Value,&value)!=nil || value.PID!=before.PID || !value.OldDenied{t.Fatal("missing actual fresh cleanup scope",method,err,string(row.Value))}
        if method=="stop" && strings.Join(value.Order,",")!="close,stop_service,stop"{t.Fatal("actual source cleanup order",value.Order)}
    }
    if _,err:=a.pluginStore.Data(context.Background(),ownedConcurrentAppID,"unauthorized_old");err==nil{t.Fatal("old callback borrowed cleanup authority")}
    ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalConcurrentAppHTTPExceedsThirtySecondsAndHonorsCaller(t *testing.T){
    a,_,object:=ownedConcurrentAppFixture(t);h:=a.Handler();before:=ownedConcurrentAppSnapshot(t,object)
    ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);defer cancel()
    started:=time.Now()
    state:=ownedConcurrentAppResult(t,ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"long","beyond-thirty",""))
    elapsed:=time.Since(started)
    if elapsed<=30*time.Second || state.PID!=before.PID{t.Fatal("real business call did not cross old 30s gate on same object",elapsed,state)}
    t.Logf("actual artifact HTTP call completed after %s on worker pid=%d, beyond former 30s budget",elapsed,state.PID)
    short,shortCancel:=context.WithTimeout(context.Background(),500*time.Millisecond);defer shortCancel()
    failed:=ownedConcurrentAppHTTP(short,h,ownedConcurrentAppID,"long","caller-deadline","")
    if failed.Code==http.StatusOK || short.Err()!=context.DeadlineExceeded{t.Fatal("real caller deadline not honored",failed.Code,short.Err())}
    ownedConcurrentAppWaitData(t,a,ownedConcurrentAppID,"entered_caller-deadline")
    state=ownedConcurrentAppResult(t,ownedConcurrentAppHTTP(ctx,h,ownedConcurrentAppID,"state","after-deadline",""))
    if state.PID!=before.PID{t.Fatal("deadline killed/restarted entire artifact")}
    ownedConcurrentAppNoRuns(t,a)
}

func TestOriginalConcurrentAppCloneArtifactAndDataIsolation(t *testing.T){
    a,cookie,source:=ownedConcurrentAppFixture(t)
    update:=callTest(t,a.Handler(),"PUT","/api/plugins/"+ownedConcurrentAppID,`{"enabled":true,"key":"source"}`,cookie,"")
    assertOriginalRegistrySuccess(t,update.Code,update.Body.Bytes());a.wg.Wait()
    before:=ownedConcurrentAppSnapshot(t,source)
    response:=callTest(t,a.Handler(),"POST","/api/plugins/"+ownedConcurrentAppID+"/clone",`{"suffix":"Twin"}`,cookie,"")
    assertOriginalRegistrySuccess(t,response.Code,response.Body.Bytes());a.wg.Wait()
    cloneID:=ownedConcurrentAppID+"twin"
    value,ok:=a.pluginRunningInstances.Load(cloneID);if !ok{t.Fatal("actual clone absent")};clone:=value.(*originalPluginInstance)
    clone.sessionMu.Lock();client:=clone.session;clone.sessionMu.Unlock()
    if client==nil || !client.SupportsConcurrent(){t.Fatal("clone did not negotiate its own worker")}
    t.Cleanup(func(){a.stopOriginalPlugin(context.Background(),cloneID);if err:=client.Stop(context.Background());err!=nil{t.Error("clone child exit/race",err)}})
    changed:=callTest(t,a.Handler(),"PUT","/api/plugins/"+cloneID,`{"enabled":true,"key":"clone"}`,cookie,"")
    assertOriginalRegistrySuccess(t,changed.Code,changed.Body.Bytes());a.wg.Wait()
    original,other:=ownedConcurrentAppSnapshot(t,source),ownedConcurrentAppSnapshot(t,clone)
    if original.PID!=before.PID || original.PID==other.PID || original.Path==other.Path || string(original.Value)!=`"source"` || string(other.Value)!=`"clone"`{t.Fatal("clone object/data/resource scope shared",original,other)}
    sourceArtifact,err:=artifacts.ReadInstalled(a.pluginPackageRoot(),ownedConcurrentAppID,validatePluginArtifactContract);if err!=nil{t.Fatal(err)}
    cloneArtifact,err:=artifacts.ReadInstalled(a.pluginPackageRoot(),cloneID,validatePluginArtifactContract);if err!=nil{t.Fatal(err)}
    first,e1:=os.Stat(sourceArtifact.ExecutablePath);second,e2:=os.Stat(cloneArtifact.ExecutablePath)
    if e1!=nil || e2!=nil || sourceArtifact.SHA256!=cloneArtifact.SHA256 || os.SameFile(first,second){t.Fatal("clone artifact is not independent equal bytes",e1,e2)}
    ownedConcurrentAppNoRuns(t,a)
}
