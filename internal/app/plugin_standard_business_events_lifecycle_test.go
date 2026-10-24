package app

import (
    "context"
    "fmt"
    "reflect"
    "runtime"
    "strings"
    "sync"
    "testing"
    "time"
)

// This observer is test-only. An entry/cancel signal followed by select-default
// cannot establish that Close actually reached its Wait. Inspect the exact
// closer goroutine until it is blocked in sync.WaitGroup.Wait. The deadline is
// only a failure guard; elapsed time is never a passing observation.
func waitStandardCloseAtWait(ctx context.Context, goroutineID string, closed <-chan struct{}) error {
    stack := make([]byte, 2<<20)
    prefix := "goroutine " + goroutineID + " ["
    for {
        select {
        case <-closed:
            return fmt.Errorf("actual App.Close returned before the admitted task was released")
        case <-ctx.Done():
            return fmt.Errorf("did not observe the exact App.Close goroutine waiting for its admitted task: %w", ctx.Err())
        default:
        }
        n := runtime.Stack(stack, true)
        if n == len(stack) { return fmt.Errorf("goroutine stack snapshot truncated") }
        for _, block := range strings.Split(string(stack[:n]), "\n\n") {
            if strings.HasPrefix(block, prefix) && strings.Contains(block, "sync.(*WaitGroup).Wait(") && strings.Contains(block, "light-media/internal/app.(*App).Close(") {
                return nil
            }
        }
        runtime.Gosched()
    }
}

func TestStandardBusinessEventsBackgroundAdmissionSealsBeforeActualCloseWait(t *testing.T) {
    a, err := New(Options{DataDir:t.TempDir()})
    if err != nil { t.Fatal(err) }
    release := make(chan struct{})
    taskStarted := make(chan struct{})
    taskFinished := make(chan struct{})
    closeStarted := make(chan string,1)
    closed := make(chan struct{})
    var releaseOnce sync.Once
    var closeErr error
    closerStarted := false
    t.Cleanup(func() {
        releaseOnce.Do(func() { close(release) })
        if !closerStarted { _=a.Close(); return }
        select {
        case <-closed:
        case <-time.After(10*time.Second):
            t.Error("actual App.Close did not terminate after admission task cleanup")
        }
    })
    if !a.admitOriginalBackground() { t.Fatal("initial live admission rejected") }
    go func() {
        close(taskStarted)
        <-release
        close(taskFinished)
        a.wg.Done()
    }()
    <-taskStarted
    closerStarted=true
    go func() {
        stack := make([]byte,256)
        n := runtime.Stack(stack,false)
        fields := strings.Fields(string(stack[:n]))
        id := ""
        if len(fields)>1 && fields[0]=="goroutine" { id=fields[1] }
        closeStarted<-id
        closeErr=a.Close()
        close(closed)
    }()
    goroutineID:=<-closeStarted
    if goroutineID=="" { t.Fatal("could not identify actual Close goroutine") }
    ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
    defer cancel()
    if err=waitStandardCloseAtWait(ctx,goroutineID,closed);err!=nil { t.Fatal(err) }
    // Close itself, not this test, sealed admission before reaching Wait.
    // A broken admission increments the counter; balance only that accidental
    // increment so cleanup still makes progress before reporting the failure.
    if a.admitOriginalBackground() {
        a.wg.Done()
        t.Fatal("new background task admitted after actual Close sealed admission")
    }
    select {
    case <-taskFinished: t.Fatal("admitted task completed without its channel release")
    default:
    }
    select {
    case <-closed: t.Fatal("Close passed its observed Wait while the admitted task remained blocked")
    default:
    }
    releaseOnce.Do(func() { close(release) })
    select {
    case <-closed:
    case <-ctx.Done(): t.Fatal("Close did not finish after admitted task Done",ctx.Err())
    }
    select {
    case <-taskFinished:
    default: t.Fatal("Close returned before admitted task completion")
    }
    if closeErr!=nil { t.Fatal(closeErr) }
    if a.admitOriginalBackground() {
        a.wg.Done()
        t.Fatal("admission reopened after Close returned")
    }
}

func TestStandardBusinessEventsActualCoreCommitsAfterAdmissionSealWithoutCallbacks(t *testing.T) {
    a:=newTestApp(t)
    cookie:=installOwnedEventFixture(t,a)
    first:=identityTestSite("Owned before seal","owned-admission.example.com")
    if identityHTTP(t,a,cookie,"POST","/api/sites/manage",first)!=200 { t.Fatal("initial actual core create") }
    requireBusinessEventCount(t,a,ownedEventFixtureID,"site.updated",1)
    first=a.Settings().Sites[0]
    firstIdentity:=identityRequire(t,a,first.ID)
    before:=ownedBusinessEvents(t,a,ownedEventFixtureID)
    loaded,ok:=a.pluginRunningInstances.Load(ownedEventFixtureID)
    if !ok || !loaded.(*originalPluginInstance).external { t.Fatal("actual external SDK subscriber absent before seal") }

    // Explicit phase boundary only. Store and live plugin remain available;
    // this does not pretend to test whole-HTTP-request vs full Close races.
    a.closeOriginalBackgroundAdmission()
    second:=identityTestSite("Owned after seal","second-admission.example.net")
    if identityHTTP(t,a,cookie,"POST","/api/sites/manage",second)!=200 { t.Fatal("seal changed core creation semantics") }
    if len(a.Settings().Sites)!=2 { t.Fatal("post-seal creation did not commit") }
    first.Name="Owned post-seal edit"
    if identityHTTP(t,a,cookie,"POST","/api/sites/manage",first)!=200 { t.Fatal("seal changed core editing semantics") }
    found:=false
    for _,site:=range a.Settings().Sites { if site.ID==first.ID { found=site.Name==first.Name } }
    if !found { t.Fatal("post-seal update did not commit") }
    if identityHTTP(t,a,cookie,"DELETE","/api/sites/manage/"+first.ID,nil)!=200 { t.Fatal("seal changed core deletion semantics") }
    if len(a.Settings().Sites)!=1 || a.Settings().Sites[0].Name!=second.Name { t.Fatal("post-seal deletion did not publish settings") }
    retired:=identityRequire(t,a,first.ID)
    if retired.State!="deleted" || retired.SourceID!=firstIdentity.SourceID { t.Fatal("post-seal deletion lost its committed original identity") }
    var facts int
    if err:=a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_id=?`,domainEventID("site.deleted",first.ID)).Scan(&facts);err!=nil || facts!=1 { t.Fatal("post-seal core deletion fact was not committed",facts,err) }
    after:=ownedBusinessEvents(t,a,ownedEventFixtureID)
    if !reflect.DeepEqual(before,after) { t.Fatal("post-seal actual core producers reached SDK event callbacks") }
    requireBusinessEventCount(t,a,ownedEventFixtureID,"site.updated",1)
    requireBusinessEventCount(t,a,ownedEventFixtureID,"site.deleted",0)
    current,ok:=a.pluginRunningInstances.Load(ownedEventFixtureID)
    if !ok || current!=loaded { t.Fatal("seal test passed by removing/replacing the SDK object") }
    object:=current.(*originalPluginInstance)
    object.configMu.RLock()
    enabled:=object.eventsEnabled
    object.configMu.RUnlock()
    if !enabled { t.Fatal("seal test passed by disabling the subscriber instead of rejecting admission") }
}
