package plugins

import (
 "context"
 "encoding/json"
 "io"
)

// SupportsTransportClose is immutable, advertised SDK transport support, not
// manifest authority. Older concurrent workers retain their existing API.
func (c *Client) SupportsTransportClose() bool { return c != nil && c.concurrent != nil && c.concurrent.transportClose }

// CloseTransport retires an unpublished process without invoking any plugin
// lifecycle method, including "stop". An explicit transport-only fence makes
// the ensuing stdin EOF an intentional shutdown. Handlers are canceled/joined
// and the normal child exit is observed. Only a deadline forces termination.
// It shares Stop's single final join state; legacy/old SDKs are unsupported.
func (c *Client) CloseTransport(ctx context.Context) error {
 if ctx == nil { return failure("invalid_invocation") }
 if !c.SupportsTransportClose() { return failure("transport_close_unsupported") }
 c.stopRequested.Store(true)
 m:=c.concurrent
 m.mu.Lock()
 if m.stopDone!=nil {
  done:=m.stopDone;m.mu.Unlock()
  select{case <-done:m.mu.Lock();err:=m.stopErr;m.mu.Unlock();return err;case <-ctx.Done():return failure("execution_canceled")}
 }
 m.draining=true;m.stopDone=make(chan struct{})
 for _,call:=range m.calls{call.revoke()}
 m.mu.Unlock()
 bounded,cancel:=context.WithTimeout(ctx,c.runner.Timeout);defer cancel()
 out,err:=m.call(bounded,"transport.retire",json.RawMessage(`{"version":1}`),nil,CallOptions{Timeout:c.runner.Timeout},true)
 var acknowledged struct{Version int `json:"version"`}
 if err==nil&&(strictJSON(out.Result,&acknowledged)!=nil||acknowledged.Version!=1){err=failure("concurrent_session_mismatch")}
 m.mu.Lock();m.stopping=true;m.mu.Unlock()
 // The ACK follows every previously emitted worker host_call. New host
 // requests are now revoked, so no callback/writer can be added after this
 // fence. Drain complete logical writes before closing the input pipe.
 joined:=make(chan struct{})
 go func(){m.writes.Wait();m.hosts.Wait();close(joined)}()
 if err==nil {select{case <-joined:case <-bounded.Done():err=failure("execution_canceled")}}
 if err==nil {
  m.mu.Lock();m.expectedEOF=true;m.mu.Unlock()
  if closer,ok:=c.session.writer.(io.Closer);ok{err=closer.Close()}else{err=failure("invalid_worker")}
 }
 if err==nil {
  select{case <-c.done:if c.waitErr!=nil{err=failure("worker_failed")};case <-bounded.Done():err=failure("execution_canceled")}
 }
 if err==nil {
  // Observe genuine worker EOF before canceling our own reader context.
  <-m.readerDone
  m.mu.Lock();err=m.terminal;m.mu.Unlock()
 }
 if err!=nil {m.abort(err)}else{c.cancel();c.session.abort()}
 // Observe the one actual reaper even on timeout/forced termination. The
 // existing command WaitDelay bounds pipe cleanup after the kill fallback.
 <-c.done
 <-m.readerDone
 select{case <-joined:case <-bounded.Done():if err==nil{err=failure("execution_canceled")}}
 if bytes,exceeded:=c.stderr.snapshot();bytes!=0||exceeded{err=failure("worker_failed")}
 m.mu.Lock();m.stopErr=err;close(m.stopDone);m.mu.Unlock()
 return err
}
