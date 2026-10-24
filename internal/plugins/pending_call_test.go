package plugins

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnedPendingCallActualAdmissionAndFullCapacity(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent:true})
	ctx := ownedContext(t)
	entered := make(chan struct{},32)
	release := make(chan struct{})
	defer close(release)
	var calls []*PendingCall
	for i:=0;i<32;i++ {
		p,err:=c.StartCall(ctx,"invoke",ownedInput("host",json.RawMessage(`"held"`)),func(ctx context.Context,_ Identity,_ string,_ json.RawMessage)(json.RawMessage,error){
			entered<-struct{}{}
			select{case <-release:return json.RawMessage(`"held"`),nil;case <-ctx.Done():return nil,ctx.Err()}
		},CallOptions{CallerDeadlineOnly:true})
		if err!=nil {t.Fatal("admission failed before the existing resource limit",i,err)}
		calls=append(calls,p)
	}
	for i:=0;i<32;i++ {ownedAwait(t,entered)}
	started:=time.Now()
	if p,err:=c.StartCall(ctx,"invoke",ownedInput("state",nil),nil,CallOptions{CallerDeadlineOnly:true});err==nil || p!=nil {t.Fatal("full table fabricated acceptance")}
	if time.Since(started)>time.Second {t.Fatal("full-table admission waited for its own parent")}
	for _,p:=range calls {p.Cancel()}
	for _,p:=range calls {if _,err:=p.Wait();err==nil {t.Fatal("canceled pending call succeeded")}}
}

func TestOwnedPendingCallLostParentReplyDoesNotRollbackChild(t *testing.T) {
	c:=ownedConcurrentStart(t,Runner{RequireConcurrent:true})
	parentCtx,cancel:=context.WithCancel(ownedContext(t));defer cancel()
	accepted:=make(chan *PendingCall,1)
	childEntered:=make(chan struct{})
	releaseChild:=make(chan struct{})
	defer close(releaseChild)
	parentDone:=make(chan error,1)
	var children atomic.Int32
	go func(){
		_,err:=c.Call(parentCtx,"invoke",ownedInput("host",nil),func(ctx context.Context,_ Identity,_ string,_ json.RawMessage)(json.RawMessage,error){
			parentID:=InvocationID(ctx)
			pending,err:=c.StartCallWithAdmission(ctx,context.Background(),"invoke",ownedInput("host",json.RawMessage(`"child"`)),func(childCtx context.Context,_ Identity,_ string,_ json.RawMessage)(json.RawMessage,error){
				if InvocationID(childCtx)=="" || InvocationID(childCtx)==parentID {t.Error("child reused parent invocation identity")}
				children.Add(1);close(childEntered)
				select{case <-releaseChild:return json.RawMessage(`"child"`),nil;case <-childCtx.Done():return nil,childCtx.Err()}
			},CallOptions{CallerDeadlineOnly:true})
			if err!=nil{return nil,err}
			accepted<-pending
			// Deliberately lose the actual host_result after successful SDK
			// admission. No receipt API or automatic retry is involved.
			<-ctx.Done()
			return nil,ctx.Err()
		})
		parentDone<-err
	}()
	var child *PendingCall
	select{case child=<-accepted:case <-time.After(3*time.Second):t.Fatal("parent callback did not admit child")}
	ownedAwait(t,childEntered)
	cancel()
	select{case err:=<-parentDone:if err==nil{t.Fatal("lost parent reply reported success")};case <-time.After(3*time.Second):t.Fatal("parent cancellation blocked")}
	select{case <-child.done:t.Fatal("parent cancellation revoked independently accepted child");default:}
	// Explicit single-child cancellation is still available to its owner.
	child.Cancel()
	if _,err:=child.Wait();err==nil{t.Fatal("explicit child cancellation ignored")}
	if children.Load()!=1{t.Fatal("lost reply was retried",children.Load())}
	_ = ownedState(t,c)
}

func TestOwnedPendingCallCanceledAdmissionAndLegacy(t *testing.T) {
	c:=ownedConcurrentStart(t,Runner{RequireConcurrent:true})
	canceled,cancel:=context.WithCancel(context.Background());cancel()
	before:=ownedState(t,c)
	if p,err:=c.StartCallWithAdmission(canceled,context.Background(),"invoke",ownedInput("state",nil),nil,CallOptions{CallerDeadlineOnly:true});err==nil || p!=nil{t.Fatal("canceled admission accepted")}
	after:=ownedState(t,c)
	if after.Calls!=before.Calls+1{t.Fatal("rejected admission reached actual worker",before,after)}
	legacy:=ownedConcurrentStart(t,Runner{},"--legacy")
	if p,err:=legacy.StartCall(context.Background(),"invoke",ownedInput("state",nil),nil,CallOptions{});err==nil || p!=nil{t.Fatal("legacy falsely accepted asynchronous call")}
	if legacy.SupportsTransportClose() || legacy.CloseTransport(context.Background())==nil{t.Fatal("legacy falsely accepted transport retirement")}
	_ = ownedState(t,legacy)
}

func TestOwnedPendingCallTransportRetirementInvokesNoHandler(t *testing.T) {
	c:=ownedConcurrentStart(t,Runner{RequireConcurrent:true},"--cleanup-host")
	before:=ownedState(t,c)
	if before.PID==0{t.Fatal("missing actual process")}
	if err:=c.CloseTransport(ownedContext(t));err!=nil{t.Fatal("transport-only retirement failed",err)}
	select{case <-c.done:default:t.Fatal("transport retirement did not reap")}
	if c.waitErr!=nil{t.Fatal("transport retirement did not observe normal child exit",c.waitErr)}
	// The fixture's stop handler requires a host call when --cleanup-host is
	// present. A normal Stop(nil) would fail, so this success is not fake Stop.
	if err:=c.Stop(ownedContext(t));err!=nil{t.Fatal("idempotent retirement result",err)}
	if n,_:=c.stderr.snapshot();n!=0{t.Fatal("worker race/diagnostic output",n)}

	uncooperative:=ownedConcurrentStart(t,Runner{RequireConcurrent:true})
	pending,err:=uncooperative.StartCall(ownedContext(t),"invoke",ownedInput("uncooperative",nil),func(context.Context,Identity,string,json.RawMessage)(json.RawMessage,error){return json.RawMessage(`null`),nil},CallOptions{CallerDeadlineOnly:true})
	if err!=nil{t.Fatal(err)}
	deadline:=time.Now().Add(3*time.Second)
	for {
		out,err:=uncooperative.Call(ownedContext(t),"invoke",ownedInput("state",nil),nil)
		var state struct{Uncooperative bool `json:"uncooperative"`}
		if err!=nil||json.Unmarshal(out.Result,&state)!=nil{t.Fatal("actual uncooperative barrier",err)}
		if state.Uncooperative{break};if time.Now().After(deadline){t.Fatal("worker did not enter noncooperative handler")};time.Sleep(5*time.Millisecond)
	}
	bounded,cancel:=context.WithTimeout(context.Background(),100*time.Millisecond);defer cancel()
	if err:=uncooperative.CloseTransport(bounded);err==nil{t.Fatal("uncooperative retirement fabricated graceful exit")}
	if uncooperative.waitErr==nil{t.Fatal("deadline fallback did not observe actual forced child exit")}
	if _,err:=pending.Wait();err==nil{t.Fatal("retired invocation retained success/authority")}
}
