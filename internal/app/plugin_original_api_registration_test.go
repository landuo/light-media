package app

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "os/exec"
 "runtime"
 "sync/atomic"
 "testing"
 "time"

 "light-media/internal/plugins"
 "light-media/internal/plugins/artifacts"
)

func TestPluginOriginalAPIRegistrationActualHTTPAndClone(t *testing.T) {
	binary := os.Getenv("ORIGINAL_API_REGISTRATION_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("ORIGINAL_API_REGISTRATION_FIXTURE_BINARY must name the actual owned compiled SDK artifact")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual executable manifest: %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != "OriginalAPIRegistrationFixture" || manifest.Version != "1.0" || len(manifest.Capabilities) != 0 {
		t.Fatalf("unexpected owned manifest: %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginPureSDKContract}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/original-api-registration-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/original-api-registration-fixture"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), manifest.PluginID)
	if err != nil || !in.Installed || in.Version != manifest.Version {
		t.Fatalf("actual installed artifact: %+v %v", in, err)
	}
	loaded, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("artifact installed but not loaded")
	}
	object := loaded.(*originalPluginInstance)
	if !object.external {
		t.Fatal("fixture borrowed builtin implementation")
	}

 settings := a.Settings()
 settings.APIToken = "synthetic-original-registration-key"
 if err := a.saveSettings(context.Background(), settings); err != nil { t.Fatal(err) }
 a.Start(context.Background())
 current,ok:=a.pluginRunningInstances.Load(manifest.PluginID)
 if !ok {t.Fatal("source not loaded after app start")}
 object=current.(*originalPluginInstance)
 serverHTTP := httptest.NewServer(a.Handler())
 defer serverHTTP.Close()
 client := serverHTTP.Client()
 client.Timeout = 20*time.Second
 request := func(instance, path, method string, key bool, status int) map[string]any {
  t.Helper()
  req, err := http.NewRequest(method, serverHTTP.URL+"/api/v1/plugin/"+instance+path, nil)
  if err != nil { t.Fatal(err) }
  if key { req.Header.Set("X-API-KEY", settings.APIToken) }
  resp, err := client.Do(req)
  if err != nil { t.Fatal(err) }
  defer resp.Body.Close()
  var result map[string]any
  if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { t.Fatal(err) }
  if resp.StatusCode != status { t.Fatalf("%s %s%s: got %d body %+v want %d", method,instance,path,resp.StatusCode,result,status) }
  return result
 }
 setMode := func(p *originalPluginInstance, mode string) {
  t.Helper()
  raw, _ := json.Marshal(mode)
  if _,err := p.callSession(context.Background(),plugins.Runner{Timeout:10*time.Second},"set_mode",raw,nil);err!=nil {t.Fatal(err)}
  a.registerOriginalPluginAPIs(p.installation.InstanceID)
 }
 expectedPIDs:=map[string]float64{}
 recordPID:=func(p *originalPluginInstance) {
  t.Helper()
  var pid float64
  if err:=p.readSession(context.Background(),"get_pid",&pid);err!=nil {t.Fatal(err)}
  expectedPIDs[p.installation.InstanceID]=pid
 }
 recordPID(object)
 assertBound := func(instance,path,method string) {
  t.Helper()
  result:=request(instance,path,method,true,200)
  if result["pid"]!=expectedPIDs[instance] || result["path"]!=path || result["method"]!=method {t.Fatalf("wrong actual object binding: %+v",result)}
 }
 source:=manifest.PluginID
 a.registerOriginalPluginAPIs(source)
 request(source,"/declared","POST",true,404)
 request(source,"/actual","GET",false,401)
 request(source,"/actual","POST",true,405)
 assertBound(source,"/actual","GET")
 assertBound(source,"/actual","PATCH")
 // Clone through the real management API; independently loaded process state.
 cloneResponse:=callTest(t,a.Handler(),"POST","/api/plugins/"+source+"/clone",`{"suffix":"Bound"}`,cookie,"")
 assertOriginalRegistrySuccess(t,cloneResponse.Code,cloneResponse.Body.Bytes())
 clone:=source+"bound"
 loadedClone,ok:=a.pluginRunningInstances.Load(clone)
 if !ok {t.Fatal("real clone not loaded")}
 cloneObject:=loadedClone.(*originalPluginInstance)
 recordPID(cloneObject)
 if expectedPIDs[clone]==expectedPIDs[source] {t.Fatal("clone borrowed source process")}
 assertBound(clone,"/actual","GET")
 setMode(object,"changed")
 request(source,"/actual","GET",true,404)
 request(source,"/actual_again","GET",false,401)
 assertBound(source,"/actual_again","GET")
 assertBound(clone,"/actual","GET")
 request(clone,"/actual_again","GET",true,404)
 for _,mode:=range []string{"error","null"} {
  setMode(object,mode)
  request(source,"/actual_again","GET",true,404)
  request(source,"/declared","POST",true,404)
  assertBound(clone,"/actual","GET")
  setMode(object,"changed")
  assertBound(source,"/actual_again","GET")
 }
 setMode(cloneObject,"changed")
 request(clone,"/actual","GET",true,404)
 assertBound(clone,"/actual_again","PATCH")
 assertBound(source,"/actual_again","GET")
 if len(object.manifest.Capabilities)!=0 || len(cloneObject.manifest.Capabilities)!=0 {t.Fatal("HTTP route data granted host capabilities")}
}

